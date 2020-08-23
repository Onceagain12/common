package retry

import (
	"context"
	"io"
	"math"
	"net"
	"net/url"
	"syscall"
	"time"

	"github.com/docker/distribution/registry/api/errcode"
	errcodev2 "github.com/docker/distribution/registry/api/v2"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// RetryOptions defines the option to retry
type RetryOptions struct {
	MaxRetry int // The number of times to possibly retry
}

// RetryIfNecessary retries the operation in exponential backoff with the retryOptions
func RetryIfNecessary(ctx context.Context, operation func() error, retryOptions *RetryOptions) error {
	err := operation()
	for attempt := 0; err != nil && isRetryable(err) && attempt < retryOptions.MaxRetry; attempt++ {
		delay := time.Duration(int(math.Pow(2, float64(attempt)))) * time.Second
		logrus.Infof("Warning: failed, retrying in %s ... (%d/%d)", delay, attempt+1, retryOptions.MaxRetry)
		select {
		case <-time.After(delay):
			break
		case <-ctx.Done():
			return err
		}
		err = operation()
	}
	return err
}

func isRetryable(err error) bool {
	err = errors.Cause(err)

	if err == context.Canceled || err == context.DeadlineExceeded {
		return false
	}

	type unwrapper interface {
		Unwrap() error
	}

	switch e := err.(type) {

	case errcode.Error:
		switch e.Code {
		case errcode.ErrorCodeUnauthorized, errcodev2.ErrorCodeNameUnknown, errcodev2.ErrorCodeManifestUnknown:
			return false
		}
		return true
	case *net.OpError:
		return isRetryable(e.Err)
	case *url.Error: // This includes errors returned by the net/http client.
		if e.Err == io.EOF { // Happens when a server accepts a HTTP connection and sends EOF
			return true
		}
		return isRetryable(e.Err)
	case syscall.Errno:
		return e != syscall.ECONNREFUSED
	case errcode.Errors:
		// if this error is a group of errors, process them all in turn
		for i := range e {
			if !isRetryable(e[i]) {
				return false
			}
		}
		return true
	case *multierror.Error:
		// if this error is a group of errors, process them all in turn
		for i := range e.Errors {
			if !isRetryable(e.Errors[i]) {
				return false
			}
		}
		return true
	case unwrapper:
		err = e.Unwrap()
		return isRetryable(err)
	}

	return false
}
