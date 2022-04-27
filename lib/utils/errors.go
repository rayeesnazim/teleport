/*
Copyright 2021 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"errors"
	"strings"
	"syscall"

	"github.com/gravitational/teleport/api/constants"
	"github.com/gravitational/trace"
)

// IsUseOfClosedNetworkError returns true if the specified error
// indicates the use of closed network connection
// TODO(dmitri): replace in go1.16 with `errors.Is(err, net.ErrClosed)`
func IsUseOfClosedNetworkError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), constants.UseOfClosedNetworkConnection)
}

// IsFailedToSendCloseNotifyError returns true if the provided error is the
// "tls: failed to send closeNotify".
func IsFailedToSendCloseNotifyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), constants.FailedToSendCloseNotify)
}

// IsOKNetworkError returns true if the provided error received from a network
// operation is one of those that usually indicate normal connection close.
func IsOKNetworkError(err error) bool {
	return trace.IsEOF(err) || IsUseOfClosedNetworkError(err) || IsFailedToSendCloseNotifyError(err)
}

// IsConnectionRefused returns true if the given err is "connection refused" error.
func IsConnectionRefused(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.ECONNREFUSED
	}
	return false
}

// HasTraceType checks if the error was wrapped with a trace type.
func HasTraceType(err error) bool {
	return trace.IsNotFound(err) ||
		trace.IsBadParameter(err) ||
		trace.IsAlreadyExists(err) ||
		trace.IsLimitExceeded(err) ||
		trace.IsConnectionProblem(err) ||
		trace.IsNotImplemented(err) ||
		trace.IsCompareFailed(err) ||
		trace.IsOAuth2(err) ||
		trace.IsAccessDenied(err)
}
