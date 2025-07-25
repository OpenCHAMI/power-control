# MIT License
#
# (C) Copyright [2021-2023] Hewlett Packard Enterprise Development LP
#
# Permission is hereby granted, free of charge, to any person obtaining a
# copy of this software and associated documentation files (the "Software"),
# to deal in the Software without restriction, including without limitation
# the rights to use, copy, modify, merge, publish, distribute, sublicense,
# and/or sell copies of the Software, and to permit persons to whom the
# Software is furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included
# in all copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.  IN NO EVENT SHALL
# THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
# OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
# ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
# OTHER DEALINGS IN THE SOFTWARE.

# Service
NAME    ?= cray-power-control
VERSION ?= $(shell git describe --tags --always --abbrev=0)
STORAGE ?= POSTGRES

all: image unittest integration snyk ct ct_image

image:
	docker build --pull ${DOCKER_ARGS} --tag '${NAME}:${VERSION}' .

# (Mostly) incorrectly-named integration tests. Some actual unit tests.
# These spawn supporting containers via docker-compose and invoke "go test" from a test container in the compose network.
unittest:
	STORAGE=${STORAGE} ./runUnitTest.sh

# Integration tests that spawn their own containers from Go.
integration:
	PCS_TEST_STORAGE=${STORAGE} go test --tags=integration_tests ./...

snyk:
	./runSnyk.sh

ct:
	./runCT.sh

ct_image:
	docker build --no-cache -f test/ct/Dockerfile test/ct/ --tag cray-power-control-hmth-test:${VERSION}

image-pprof:
	docker build --pull ${DOCKER_ARGS} --tag '${NAME}-pprof:${VERSION}' -f Dockerfile.pprof .

