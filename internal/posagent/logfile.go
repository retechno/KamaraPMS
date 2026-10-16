package posagent

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RotatingFile is a log file that moves itself aside when it grows past Max bytes and keeps Keep older files (agent.log.1 ... agent.log.N).
type RotatingFile struct {
	Path string
	Max  int64
	Keep int

	mu   sync.Mutex
	f    *os.File
	size int64
}

// NewRotatingFile opens the log, creating its directory.
func NewRotatingFile(path string, maxBytes int64, keep int) (*RotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	r := &RotatingFile{Path: path, Max: maxBytes, Keep: keep}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // G304: the log is beside the configuration
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

// Write appends to the log, rotating first when the line would take it past the limit.
func (r *RotatingFile) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.Max > 0 && r.size+int64(len(b)) > r.Max {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(b)
	r.size += int64(n)
	return n, err
}

func (r *RotatingFile) rotate() error {
	_ = r.f.Close()
	r.f = nil
	for i := r.Keep; i >= 1; i-- {
		from := r.Path
		if i > 1 {
			from = fmt.Sprintf("%s.%d", r.Path, i-1)
		}
		to := fmt.Sprintf("%s.%d", r.Path, i)
		if i == r.Keep {
			_ = os.Remove(to)
		}
		if _, err := os.Stat(from); err == nil {
			_ = os.Rename(from, to)
		}
	}
	return r.open()
}

// Close closes the file.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
