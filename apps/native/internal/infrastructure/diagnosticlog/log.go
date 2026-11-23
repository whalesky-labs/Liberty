package diagnosticlog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/fileout"
)

const Filename = "desktop-pet-native-diagnostics.log"
const MaxBytes = 256 << 10
const maxEntries = 1024

type Event uint8

const (
	SessionStarted Event = iota + 1
	SessionEnded
	HistoryDiscarded
	PersistenceFailed
	WindowShown
	WindowClosed
	PointerPressed
	DragStarted
	DragFinished
	PointerCancelled
	Tapped
	PositionSaved
	PositionSaveFailed
)

var eventNames = [...]string{
	"", "session_started", "session_ended", "history_discarded", "persistence_failed",
	"window_shown", "window_closed", "pointer_pressed", "drag_started", "drag_finished",
	"pointer_cancelled", "tapped", "position_saved", "position_save_failed",
}

type Instance uint8

const (
	Application Instance = iota
	Primary
	Extra
)

var instanceNames = [...]string{"application", "primary", "extra"}

type entry struct {
	Time     string `json:"time"`
	Event    string `json:"event"`
	Instance string `json:"instance"`
}

type Log struct {
	mu                sync.Mutex
	target            string
	write             func(string, []byte) error
	wake              chan struct{}
	done              chan struct{}
	lines             [][]byte
	size              int
	dirty             bool
	persistenceFailed bool
	closed            bool
}

func Open(directory string) *Log {
	return openWithWriter(directory, func(target string, data []byte) error {
		return fileout.Write(context.Background(), target, data)
	})
}

func openWithWriter(directory string, write func(string, []byte) error) *Log {
	log := &Log{write: write, wake: make(chan struct{}, 1), done: make(chan struct{})}
	canonical, err := filepath.EvalSymlinks(directory)
	if err == nil && filepath.IsAbs(canonical) {
		log.target = filepath.Join(canonical, Filename)
		if err := log.load(canonical); err != nil {
			log.lines, log.size = nil, 0
			log.append(HistoryDiscarded, Application)
		}
	}
	log.append(SessionStarted, Application)
	go log.persist()
	log.notify()
	return log
}

func (log *Log) notify() {
	select {
	case log.wake <- struct{}{}:
	default:
	}
}

func valid(event Event, instance Instance) bool {
	if event == 0 || int(event) >= len(eventNames) || int(instance) >= len(instanceNames) {
		return false
	}
	if event <= PersistenceFailed {
		return instance == Application
	}
	return instance != Application
}

func (log *Log) Record(event Event, instance Instance) {
	if !valid(event, instance) {
		return
	}
	log.mu.Lock()
	if log.closed {
		log.mu.Unlock()
		return
	}
	log.append(event, instance)
	log.mu.Unlock()
	log.notify()
}

func (log *Log) append(event Event, instance Instance) {
	log.add(entry{time.Now().UTC().Format(time.RFC3339Nano), eventNames[event], instanceNames[instance]})
	log.dirty = true
}

func (log *Log) add(value entry) {
	data, _ := json.Marshal(value)
	data = append(data, '\n')
	log.lines = append(log.lines, data)
	log.size += len(data)
	for len(log.lines) > maxEntries || log.size > MaxBytes {
		log.size -= len(log.lines[0])
		copy(log.lines, log.lines[1:])
		log.lines[len(log.lines)-1] = nil
		log.lines = log.lines[:len(log.lines)-1]
	}
}

func (log *Log) snapshot() []byte {
	return bytes.Join(log.lines, nil)
}

func (log *Log) persist() {
	defer close(log.done)
	for range log.wake {
		log.mu.Lock()
		if !log.dirty {
			closed := log.closed
			log.mu.Unlock()
			if closed {
				return
			}
			continue
		}
		data := log.snapshot()
		log.dirty = false
		log.mu.Unlock()
		failed := log.target == "" || log.write == nil || log.write(log.target, data) != nil
		log.mu.Lock()
		if failed && !log.persistenceFailed {
			log.append(PersistenceFailed, Application)
		}
		log.persistenceFailed = failed
		pending, closed := log.dirty, log.closed
		log.mu.Unlock()
		if pending {
			log.notify()
		} else if closed {
			return
		}
	}
}

func (log *Log) DiagnosticLog(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if log.closed {
		return nil, domain.ErrDiagnosticLog
	}
	return log.snapshot(), nil
}

func (log *Log) Close() {
	log.mu.Lock()
	if !log.closed {
		log.append(SessionEnded, Application)
		log.closed = true
	}
	log.mu.Unlock()
	log.notify()
	<-log.done
}

func (log *Log) load(directory string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return domain.ErrDiagnosticLog
	}
	defer root.Close()
	before, err := root.Lstat(Filename)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !before.Mode().IsRegular() || before.Size() > MaxBytes {
		return domain.ErrDiagnosticLog
	}
	file, err := root.Open(Filename)
	if err != nil {
		return domain.ErrDiagnosticLog
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() > MaxBytes {
		return domain.ErrDiagnosticLog
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxBytes+1))
	if err != nil || len(data) > MaxBytes || len(data) > 0 && data[len(data)-1] != '\n' {
		return domain.ErrDiagnosticLog
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 512), 512)
	for scanner.Scan() {
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		var value entry
		if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || !validEntry(value) {
			return domain.ErrDiagnosticLog
		}
		log.add(value)
	}
	if scanner.Err() != nil {
		return domain.ErrDiagnosticLog
	}
	return nil
}

func validEntry(value entry) bool {
	stamp, err := time.Parse(time.RFC3339Nano, value.Time)
	if err != nil || stamp.UTC().Format(time.RFC3339Nano) != value.Time {
		return false
	}
	for event, name := range eventNames {
		if name == value.Event {
			for instance, label := range instanceNames {
				if label == value.Instance {
					return valid(Event(event), Instance(instance))
				}
			}
		}
	}
	return false
}
