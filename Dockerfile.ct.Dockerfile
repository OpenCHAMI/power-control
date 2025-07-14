# MIT License
#
# (C) Copyright [2022-2024] Hewlett Packard Enterprise Development LP
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
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
# THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
# OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
# ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
# OTHER DEALINGS IN THE SOFTWARE.

# Dockerfile for building power-control.

# Build base just has the packages installed we need.
FROM docker.io/library/golang:1.24-alpine AS build-base

RUN set -ex \
    && apk -U upgrade \
    && apk add build-base

# Base copies in the files we need to test/build.
FROM build-base AS base

RUN go env -w GO111MODULE=auto

# Copy all the necessary files to the image.
COPY cmd $GOPATH/src/github.com/OpenCHAMI/power-control/v2/cmd
COPY go.mod $GOPATH/src/github.com/OpenCHAMI/power-control/v2/go.mod
COPY go.sum $GOPATH/src/github.com/OpenCHAMI/power-control/v2/go.sum
COPY internal $GOPATH/src/github.com/OpenCHAMI/power-control/v2/internal

### Build Stage ###
FROM base AS builder

RUN set -ex  && go build -C $GOPATH/src/github.com/OpenCHAMI/power-control/v2/cmd/power-control -v -tags musl -o /usr/local/bin/power-control

### Final Stage ###

FROM docker.io/alpine:3
LABEL maintainer="Hewlett Packard Enterprise"
EXPOSE 28007
STOPSIGNAL SIGTERM

RUN set -ex \
    && apk -U upgrade \
    && apk add curl jq

# Get the power-control from the builder stage.
COPY --from=builder /usr/local/bin/power-control /usr/local/bin/.
COPY configs configs
COPY migrations migrations

# Setup environment variables.
ENV SMS_SERVER="http://cray-smd:27779"
ENV LOG_LEVEL="INFO"
ENV SERVICE_RESERVATION_VERBOSITY="INFO"
ENV TRS_IMPLEMENTATION="LOCAL"
ENV STORAGE="ETCD"
ENV ETCD_HOST="etcd"
ENV ETCD_PORT="2379"
ENV HSMLOCK_ENABLED="true"
ENV VAULT_ENABLED="true"
ENV VAULT_ADDR="http://vault:8200"
ENV VAULT_KEYPATH="secret/hms-creds"

#DONT USES IN PRODUCTION; MOST WILL BREAK PROD!!!
ENV VAULT_SKIP_VERIFY="true"
ENV VAULT_TOKEN="hms"
ENV CRAY_VAULT_AUTH_PATH="auth/token/create"
ENV CRAY_VAULT_ROLE_FILE="/configs/namespace"
ENV CRAY_VAULT_JWT_FILE="/configs/token"

#nobody 65534:65534
USER 65534:65534

CMD ["sh", "-c", "power-control"]
