// Package goroutines provides the interfaces and defintions that goroutine pools must
// implement/use. Implementations are in sub-directories.
package goroutines

import "context"

// Job is a job for a Pool.
type Job func(ctx context.Context)

// SubmitOption is an option for Pool.Submit().
type SubmitOption func(opt *SubmitOptions) error

// Pool is the minimum interface that any goroutine pool must implement.
type Pool interface {
	// Submit submits a Job to be run.
	Submit(ctx context.Context, runner Job, options ...SubmitOption) error
	// Close closes the goroutine pool. This will call Wait() before it closes.
	Close()
	// Wait will wait for all goroutines to finish. This should only be called if
	// you have stopped calling Submit().
	Wait()
	// Len indicates how big the pool is.
	Len() int
	// Running returns how many goroutines are currently in flight.
	Running() int
}

// PoolType is for internal use. Please ignore.
type PoolType uint8

const (
	PTUnknown PoolType = 0
	PTPooled  PoolType = 1
	PTLimited PoolType = 2
)

// SubmitOptions is used internally. Please ignore.
type SubmitOptions struct {
	Type PoolType

	NonBlocking bool
}
