#!/usr/bin/env bash
#
# MIT License
#
# (C) Copyright [2021-2024] Hewlett Packard Enterprise Development LP
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
#

set -x

# Configure docker compose
export COMPOSE_PROJECT_NAME=$RANDOM
export COMPOSE_FILE=docker-compose.test.unit.yaml
ephCertDir=ephemeral_cert

echo "COMPOSE_PROJECT_NAME: ${COMPOSE_PROJECT_NAME}"
echo "COMPOSE_FILE: $COMPOSE_FILE"
echo "STORAGE: ${STORAGE}"


function cleanup() {
  #docker compose logs > /tmp/dockerLog_${COMPOSE_PROJECT_NAME}.txt
  rm -rf $ephCertDir
  docker compose down
  if ! [[ $? -eq 0 ]]; then
    echo "Failed to decompose environment!"
    exit 1
  fi
  exit $1
}

# Create "ephemeral" TLS .crt and .key files

mkdir -p $ephCertDir
openssl req -newkey rsa:4096 \
    -x509 -sha256 \
    -days 1 \
    -nodes \
    -subj "/C=US/ST=Minnesota/L=Bloomington/O=HPE/OU=Engineering/CN=hpe.com" \
    -out $ephCertDir/rts.crt \
    -keyout $ephCertDir/rts.key

# When running in github actions the rts.key gets created with u+rw permissions.
# This prevents RTS which runs as nobody to read the key as it doesn't have permission.
chmod o+r $ephCertDir/rts.crt $ephCertDir/rts.key

echo "Starting containers..."
docker compose build
docker compose up  -d dummy #we use dummy to make sure all our dependencies are up
docker compose ps # To improve debuggability display the current state of the conainers.
docker compose up --exit-code-from unit-tests unit-tests

test_result=$?

# Clean up
echo "Cleaning up containers..."
if [[ $test_result -ne 0 ]]; then
  echo "Unit tests FAILED!"
  cleanup 1
fi

echo "Unit tests PASSED!"
cleanup 0
