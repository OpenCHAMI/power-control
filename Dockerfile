# MIT License
#
# (C) Copyright [2020-2024] Hewlett Packard Enterprise Development LP
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
FROM chainguard/wolfi-base:latest

RUN set -ex \
    && apk update \
    && apk add --no-cache tini \
    && rm -rf /var/cache/apk/*  \
    && rm -rf /tmp/*

# Setup environment variables.
ENV SMS_SERVER=https://api-gateway.default.svc.cluster.local/apis/smd
ENV VAULT_ENABLED="true"
ENV VAULT_ADDR="http://cray-vault.vault:8200"
ENV VAULT_KEYPATH="secret/hms-creds"

ENV LOG_LEVEL="INFO"
ENV SERVICE_RESERVATION_VERBOSITY="INFO"
ENV TRS_IMPLEMENTATION="LOCAL"
ENV STORAGE="ETCD"
ENV ETCD_HOST="etcd"
ENV ETCD_PORT="2379"
ENV HSMLOCK_ENABLED="true"

ENV API_URL="http://cray-power"
ENV API_SERVER_PORT=":28007"
ENV API_BASE_PATH="/v1"

ENV PCS_JAWS_MONITOR="true"
ENV PCS_JAWS_MONITOR_INTERVAL="20"

COPY power-control /usr/local/bin/
COPY configs configs
COPY migrations migrations

#nobody 65534:65534
USER 65534:65534

CMD /usr/local/bin/power-control

ENTRYPOINT ["/sbin/tini", "--"]
