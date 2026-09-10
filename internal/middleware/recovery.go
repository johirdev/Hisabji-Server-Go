package middleware

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/logger"
	"github.com/johirdev/Hisabji-Server/internal/core/response"
)

// Recovery turns a panic into a normal 500 response envelope instead of a
// dropped connection.
//
// gin ships its own Recovery, but it writes a bare 500 with no body, no error
// code and no request id — so the client sees an empty failure and support has
// nothing to correlate. This one logs the full stack server-side, returns the
// standard envelope with the request id, and keeps the process alive.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}

			// A broken pipe means the client hung up mid-response. Nothing is
			// wrong on our side and nothing can be written back, so log it
			// quietly and stop — do not raise a 500 alert for a user who
			// closed their app.
			if isBrokenPipe(r) {
				logger.FromGin(c).Warn("client disconnected mid-response",
					"error", fmt.Sprint(r))
				c.Abort()
				return
			}

			stack := captureStack(3)
			logger.FromGin(c).Error("panic recovered",
				"panic", fmt.Sprint(r),
				"stack", stack,
			)

			err := apperr.Internal("").
				WithCause(fmt.Errorf("panic: %v", r))
			response.Fail(c, err)
		}()

		c.Next()
	}
}

func isBrokenPipe(r any) bool {
	ne, ok := r.(error)
	if !ok {
		return false
	}
	var opErr *net.OpError
	if !errors.As(ne, &opErr) {
		return errors.Is(ne, http.ErrAbortHandler)
	}
	var sysErr *os.SyscallError
	if !errors.As(opErr, &sysErr) {
		return false
	}
	msg := strings.ToLower(sysErr.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "forcibly closed") // Windows wording
}

// captureStack renders the goroutine stack, skipping the recovery plumbing so
// the first frame is the line that actually panicked.
func captureStack(skip int) string {
	const maxFrames = 24
	pcs := make([]uintptr, maxFrames)
	n := runtime.Callers(skip, pcs)
	frames := runtime.CallersFrames(pcs[:n])

	var sb strings.Builder
	for {
		frame, more := frames.Next()
		// Everything inside the Go runtime is noise for an application bug.
		if !strings.HasPrefix(frame.Function, "runtime.") {
			fmt.Fprintf(&sb, "%s\n\t%s:%d\n", frame.Function, frame.File, frame.Line)
		}
		if !more {
			break
		}
	}
	return sb.String()
}
