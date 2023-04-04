package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sync"
)

// LeaderSocketPath is the path to the (singleton) leader socket.
const LeaderSocketPath = ".buildkite-agent/agent-leader-sock"

// LeaderServer hosts the singleton Unix domain socket used for implementing
// the locking API.
type LeaderServer struct {
	mu    sync.Mutex
	locks map[string]string
	svr   *http.Server
}

// NewLeaderServer listens on the leader socket. Since the leader is the first
// process to listen on the socket path, errors of type **TODO** can be ignored.
func NewLeaderServer() (*LeaderServer, error) {
	ln, err := net.Listen("unix", LeaderSocketPath)
	if err != nil {
		return nil, err
	}
	svr := &http.Server{}
	s := &LeaderServer{
		locks: make(map[string]string),
		svr:   svr,
	}
	svr.Handler = s
	go svr.Serve(ln)
	return s, nil
}

// Shutdown calls Shutdown on the inner HTTP server, which closes the socket.
func (s *LeaderServer) Shutdown(ctx context.Context) error {
	return s.svr.Shutdown(ctx)
}

func (s *LeaderServer) load(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locks[key]
}

func (s *LeaderServer) cas(key, old, new string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks[key] == old {
		s.locks[key] = new
		return true
	}
	return false
}

// ServeHTTP serves the leader socket API.
func (s *LeaderServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("%s %s", r.Method, r.URL)

	if r.URL.Path != "/api/leader/v0/lock" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		key := r.FormValue("key")
		if key == "" {
			http.Error(w, "key parameter missing", http.StatusBadRequest)
			return
		}
		w.Write([]byte(s.load(key)))

	case http.MethodPatch:
		key, old, new := r.FormValue("key"), r.FormValue("old"), r.FormValue("new")
		if key == "" {
			http.Error(w, "key parameter missing", http.StatusBadRequest)
			return
		}
		if s.cas(key, old, new) {
			w.WriteHeader(http.StatusNoContent)
		} else {
			w.WriteHeader(http.StatusNotModified)
		}

	default:
		http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
	}
}

// LeaderClient is a client for the leader API socket.
type LeaderClient struct {
	cli *http.Client
}

// NewLeaderClient creates a new LeaderClient.
func NewLeaderClient() (*LeaderClient, error) {
	// Check the socket path exists and is a socket.
	// Note that os.ModeSocket might not be set on Windows.
	// (https://github.com/golang/go/issues/33357)
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(LeaderSocketPath)
		if err != nil {
			return nil, fmt.Errorf("stat socket: %w", err)
		}
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%q is not a socket", LeaderSocketPath)
		}
	}

	// Try to connect to the socket.
	test, err := net.Dial("unix", LeaderSocketPath)
	if err != nil {
		return nil, fmt.Errorf("socket test connection: %w", err)
	}
	test.Close()

	dialer := net.Dialer{}
	return &LeaderClient{
		cli: &http.Client{
			Transport: &http.Transport{
				// Ignore arguments, dial socket
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", LeaderSocketPath)
				},
			},
		},
	}, nil
}

// Get gets the current value of the lock key.
func (c *LeaderClient) Get(key string) (string, error) {
	u, err := url.Parse("http://agent/api/leader/v0/lock")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("key", key)
	u.RawQuery = q.Encode()

	resp, err := c.cli.Get(u.String())
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("invalid status code %d, and unable to read response body to find out more", resp.StatusCode)
		}
		return "", fmt.Errorf("invalid status code %d: %s", resp.StatusCode, b)
	}
	v, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(v), nil
}

// CompareAndSwap atomically compares-and-swaps the old value for the new value
// or performs no modification. It reports whether the new value was written.
func (c *LeaderClient) CompareAndSwap(key, old, new string) (bool, error) {
	u, err := url.Parse("http://agent/api/leader/v0/lock")
	if err != nil {
		return false, err
	}
	q := u.Query()
	q.Set("key", key)
	q.Set("old", old)
	q.Set("new", new)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodPatch, u.String(), nil)
	if err != nil {
		return false, err
	}

	resp, err := c.cli.Do(req)
	if err != nil {
		return false, err
	}
	switch resp.StatusCode {
	case http.StatusNoContent:
		return true, nil

	case http.StatusNotModified:
		return false, nil

	default:
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return false, fmt.Errorf("invalid status code %d, and unable to read response body to find out more", resp.StatusCode)
		}
		return false, fmt.Errorf("invalid status code %d: %s", resp.StatusCode, b)
	}
}
