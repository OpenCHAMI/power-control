// MIT License
//
// (C) Copyright [2020-2022,2024-2025] Hewlett Packard Enterprise Development LP
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
	"crypto/tls"
	"crypto/x509"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/sirupsen/logrus"
)

// Interface for TRS operations.

// Once there was Kafka stuff here, and now it is banished to the shadow realm, for it was never used.

//var TrsAPIContext TrsAPI

type TrsAPI interface {
	Init(serviceName string, logger *logrus.Logger) error
	SetSecurity(params interface{}) error
	CreateTaskList(source *HttpTask, numTasks int) []HttpTask
	Launch(taskList *[]HttpTask) (chan *HttpTask, error)
	Check(taskList *[]HttpTask) (running bool, err error)
	Cancel(taskList *[]HttpTask)
	Close(taskList *[]HttpTask)
	Alive() (ok bool, err error)
	Cleanup()
}

// Local operations

type TRSHTTPLocalSecurity struct {
	CACertBundleData string
	ClientCertData   string
	ClientKeyData    string
}

type clientPack struct {
	secure   *retryablehttp.Client
	insecure *retryablehttp.Client
}

type TRSHTTPLocal struct {
	Logger        *logrus.Logger
	svcName       string
	ctx           context.Context
	ctxCancelFunc context.CancelFunc
	CACertPool    *x509.CertPool
	ClientCert    tls.Certificate
	clientMap     map[ClientPolicy]*clientPack
	clientMutex   sync.Mutex
	taskMap       map[uuid.UUID]*taskChannelTuple
	taskMutex     sync.Mutex
}

type taskChannelTuple struct {
	taskListChannel chan *HttpTask
	task            *HttpTask
}

/////////////////////////////////////////////////////////////////////////////
/////////////////////////////////////////////////////////////////////////////

// Generic func to create a task array.  Called by the interface methods
// with similar names.
//
// source:  Ptr to source HTTP task descriptor.
// svcName: Service or application name.
// sender:  Sender/k8s UUID name. Can be "".
// nitems:  Number of elements to create in resulting task array.
// Return:  Array of HTTP task descriptors.

func createHTTPTaskArray(source *HttpTask,
	nitems int) []HttpTask {
	sarr := make([]HttpTask, nitems)

	for ii := 0; ii < nitems; ii++ {
		sarr[ii] = *source
		if sarr[ii].Request != nil {
			sarr[ii].Request = source.Request.Clone(context.Background())
		}
		sarr[ii].TimeStamp = time.Now().String()
		sarr[ii].id = uuid.New()
	}

	return sarr
}
