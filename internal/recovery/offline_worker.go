package recovery

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

const MigrationDBWorkerArgument = "--internal-migration-db-worker"
const workerFrameLimit = 3 * migrationLimit

type migrationDB interface {
	ReadOfflineConfig() (config.OfflineConfig, error)
	CommitOfflineMigration(config.OfflineConfig, []byte) error
	Close() error
}

type workerRequest struct {
	Operation string               `json:"operation"`
	Directory string               `json:"directory,omitempty"`
	Original  config.OfflineConfig `json:"original,omitempty"`
	Target    []byte               `json:"target,omitempty"`
}
type workerResponse struct {
	State config.OfflineConfig `json:"state"`
	Error string               `json:"error,omitempty"`
}

type migrationDBWorker struct {
	cmd     *exec.Cmd
	input   io.WriteCloser
	output  io.ReadCloser
	mu      sync.Mutex
	closed  bool
	dead    atomic.Bool
	timeout time.Duration
}

// openWorkerProcess passes configuration only through inherited anonymous
// pipes. The caller has already configured fixed unprivileged credentials.
func openWorkerProcess(cmd *exec.Cmd, dir string) (*migrationDBWorker, error) {
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	// Nil stderr goes directly to /dev/null, with no exec-managed copy pipe
	// that a surviving descendant could keep open after the worker exits.
	cmd.Stderr = nil
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	w := &migrationDBWorker{cmd: cmd, input: input, output: output, timeout: 30 * time.Second}
	if _, err := w.call(workerRequest{Operation: "open", Directory: dir}); err != nil {
		w.abort()
		return nil, err
	}
	return w, nil
}

func (w *migrationDBWorker) call(request workerRequest) (workerResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var response workerResponse
	if w.closed || w.dead.Load() {
		return response, errors.New("migration database worker is closed")
	}
	// A dead/blocked worker cannot leave the root coordinator waiting forever.
	timer := time.AfterFunc(w.timeout, w.poison)
	defer timer.Stop()
	if err := writeWorkerFrame(w.input, request); err != nil {
		w.poison()
		return response, fmt.Errorf("write migration worker request: %w", err)
	}
	if err := readWorkerFrame(w.output, &response); err != nil {
		w.poison()
		return response, fmt.Errorf("read migration worker response: %w", err)
	}
	if response.Error != "" {
		return response, fmt.Errorf("migration database worker: %s", response.Error)
	}
	return response, nil
}

func (w *migrationDBWorker) ReadOfflineConfig() (config.OfflineConfig, error) {
	r, err := w.call(workerRequest{Operation: "read"})
	return r.State, err
}

func (w *migrationDBWorker) CommitOfflineMigration(old config.OfflineConfig, target []byte) error {
	_, err := w.call(workerRequest{Operation: "commit", Original: old, Target: target})
	return err
}

func (w *migrationDBWorker) Close() error {
	w.mu.Lock()
	closed := w.closed
	w.mu.Unlock()
	if closed {
		return nil
	}
	_, err := w.call(workerRequest{Operation: "close"})
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.input.Close()
	if err != nil {
		_ = w.cmd.Process.Kill()
	}
	timer := time.AfterFunc(5*time.Second, func() { _ = w.cmd.Process.Kill() })
	defer timer.Stop()
	waitErr := w.cmd.Wait()
	w.output.Close()
	return errors.Join(err, waitErr)
}

func (w *migrationDBWorker) abort() {
	w.closed = true
	w.input.Close()
	w.output.Close()
	_ = w.cmd.Process.Kill()
	_ = w.cmd.Wait()
}

// Closing our endpoints unblocks I/O even if a worker descendant inherited its
// pipe ends. No further request may use a timed-out or desynchronized channel.
func (w *migrationDBWorker) poison() {
	w.dead.Store(true)
	w.input.Close()
	w.output.Close()
	_ = w.cmd.Process.Kill()
}

// RunMigrationDBWorker serves only after platform checks establish permanent,
// unprivileged credentials. It never opens the private root journal or helper.
func RunMigrationDBWorker() error {
	if err := hardenMigrationDBWorker(); err != nil {
		return err
	}
	return serveMigrationDBWorker(os.Stdin, os.Stdout)
}

func serveMigrationDBWorker(input io.Reader, output io.Writer) error {
	var store *config.SQLiteStore
	defer func() {
		if store != nil {
			_ = store.Close()
		}
	}()
	for {
		var req workerRequest
		if err := readWorkerFrame(input, &req); err != nil {
			return err
		}
		var response workerResponse
		var err error
		switch req.Operation {
		case "open":
			if store != nil {
				err = errors.New("database already open")
			} else {
				store, err = config.OpenOfflineMigrationStore(req.Directory)
			}
		case "read":
			if store == nil {
				err = errors.New("database is not open")
			} else {
				response.State, err = store.ReadOfflineConfig()
			}
		case "commit":
			if store == nil {
				err = errors.New("database is not open")
			} else {
				err = store.CommitOfflineMigration(req.Original, req.Target)
			}
		case "close":
			if store != nil {
				err = store.Close()
				store = nil
			}
		default:
			err = errors.New("unknown migration database operation")
		}
		if err != nil {
			response.Error = err.Error()
		}
		if err := writeWorkerFrame(output, response); err != nil {
			return err
		}
		if req.Operation == "close" {
			return nil
		}
	}
}

func writeWorkerFrame(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > workerFrameLimit {
		return errors.New("migration worker frame exceeds limit")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func readWorkerFrame(r io.Reader, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > workerFrameLimit {
		return errors.New("invalid migration worker frame size")
	}
	data := make([]byte, int(size))
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
