// Package queue provides an unbounded FIFO worker. It lets protocol read
// loops hand off work without blocking on slow consumers while preserving order.
package queue

import "sync"

// Worker runs submitted functions one at a time, in submission order.
type Worker struct {
	mu      sync.Mutex
	items   []func()
	signal  chan struct{}
	stopped bool
	done    chan struct{}
}

// NewWorker starts a worker goroutine.
func NewWorker() *Worker {
	w := &Worker{signal: make(chan struct{}, 1), done: make(chan struct{})}
	go w.loop()
	return w
}

// Submit enqueues fn. It reports false if the worker has stopped.
func (w *Worker) Submit(fn func()) bool {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return false
	}
	w.items = append(w.items, fn)
	w.mu.Unlock()
	select {
	case w.signal <- struct{}{}:
	default:
	}
	return true
}

// Stop drops queued work and stops the worker after any running function returns.
func (w *Worker) Stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	w.items = nil
	w.mu.Unlock()
	select {
	case w.signal <- struct{}{}:
	default:
	}
}

// Done is closed when the worker goroutine exits.
func (w *Worker) Done() <-chan struct{} {
	return w.done
}

func (w *Worker) loop() {
	defer close(w.done)
	for {
		w.mu.Lock()
		if w.stopped {
			w.mu.Unlock()
			return
		}
		if len(w.items) == 0 {
			w.mu.Unlock()
			<-w.signal
			continue
		}
		fn := w.items[0]
		w.items[0] = nil
		w.items = w.items[1:]
		w.mu.Unlock()
		fn()
	}
}
