package queue

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorkerRunsInOrderAndStops(t *testing.T) {
	worker := NewWorker()
	results := make(chan int, 3)
	for i := range 3 {
		assert.True(t, worker.Submit(func() { results <- i }))
	}
	for i := range 3 {
		assert.Equal(t, i, <-results)
	}
	worker.Stop()
	<-worker.Done()
	assert.False(t, worker.Submit(func() {}))
	worker.Stop()
}
