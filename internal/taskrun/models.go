// MIT License
//
// (C) Copyright [2021,2024-2025] Hewlett Packard Enterprise Development LP
//
// Permission is hereby granted, free of charge, to any person obtaining a
// copy of this software and associated documentation files (the "Software"),
// to deal in the Software without restriction, including without limitation
// the rights to use, copy, modify, merge, publish, distribute, sublicense,
// and/or sell copies of the Software, and to permit persons to whom the
// Software is furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included
// in all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
// THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
// OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
// ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
// OTHER DEALINGS IN THE SOFTWARE.

package taskrun

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type RetryPolicy struct {
	Retries        int           // number of retries to attempt (default is 3)
	BackoffTimeout time.Duration // base backoff timeout between retries (default is 5 seconds)
}

type HttpTxPolicy struct {
	Enabled               bool          // policy enabled (default is false)
	MaxIdleConns          int           // max idle connections across all hosts (default is 100)
	MaxIdleConnsPerHost   int           // max idle connections per host (default is 2)
	IdleConnTimeout       time.Duration // duration an idle connection remains open (default is unlimited)
	ResponseHeaderTimeout time.Duration // max wait time for a host's response header (default is unlimited)
	TLSHandshakeTimeout   time.Duration // max duration for the TLS handshake (default is 10 seconds)
	DisableKeepAlives     bool          // disable HTTP keep-alives if true (default is false)
}

type ClientPolicy struct {
	Retry RetryPolicy  // task's retry policy
	Tx    HttpTxPolicy // task's transport policy
}

type HttpTask struct {
	id            uuid.UUID          // message id
	ServiceName   string             // name of the service (defaults to TRSHTTPLocal.svcName)
	Request       *http.Request      // the http request
	TimeStamp     string             // time the request was created/sent RFC3339Nano
	Err           *error             // any error associated with the request
	Timeout       time.Duration      // task's context timeout (default is 30 seconds)
	CPolicy       ClientPolicy       // task's retry and transport policies
	Ignore        bool               // if true, trs will ignore this task
	context       context.Context    // task's context
	contextCancel context.CancelFunc // task's context cancellation function
	forceInsecure bool               // if true, force insecure communication
}

func (ht HttpTask) Validate() (valid bool, err error) {
	if ht.TimeStamp == "" {
		return false, errors.New("Timstamp is empty")
	} else if ht.Request == nil {
		return false, errors.New("Request is nil")
	} else if ht.ServiceName == "" {
		return false, errors.New("ServiceName is empty")
	} else if ht.id == uuid.Nil {
		return false, errors.New("ID is nil")
	}
	return true, nil
}

func (ht *HttpTask) GetID() uuid.UUID {
	return ht.id
}

func (ht *HttpTask) SetIDIfNotPopulated() uuid.UUID {
	if ht.id == uuid.Nil {
		ht.id = uuid.New()
	}
	return ht.id
}
