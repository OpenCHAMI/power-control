#!/usr/bin/env bash

#
# MIT License
#
# (C) Copyright [2022-2023] Hewlett Packard Enterprise Development LP
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
export COMPOSE_FILE=docker-compose.test.ct.yaml

echo "COMPOSE_PROJECT_NAME: ${COMPOSE_PROJECT_NAME}"
echo "COMPOSE_FILE: $COMPOSE_FILE"

function cleanup() {
  echo "Cleaning up containers..."
  if ! docker compose down --remove-orphans; then
    echo "Failed to decompose environment!"
    exit 1
  fi
  exit $1
}

function wait_for_pcs() {
  local max_attempts=30
  local container_id state health attempt

  container_id=$(docker compose ps -q power-control)
  if [[ -z $container_id ]]; then
    echo "No Power Control container found"
    return 1
  fi

  for ((attempt = 1; attempt <= max_attempts; attempt++)); do
    state=$(docker inspect --format '{{.State.Status}}' "$container_id" 2>/dev/null || true)
    health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container_id" 2>/dev/null || true)

    if [[ $health == "healthy" ]]; then
      echo "Power Control is healthy"
      return 0
    fi
    if [[ $state == "exited" || $state == "dead" ]]; then
      echo "Power Control stopped before becoming healthy (state: $state)"
      return 1
    fi

    echo "Waiting for Power Control to become healthy ($attempt/$max_attempts; state: $state, health: $health)..."
    sleep 2
  done

  echo "Timed out waiting for Power Control to become healthy"
  return 1
}


# Get the base containers running
echo "Starting containers..."
if ! docker compose build --no-cache; then
  echo "Failed to build CT containers!"
  cleanup 1
fi

if ! docker compose up -d power-control; then
  echo "Failed to start CT environment!"
  cleanup 1
fi

if ! wait_for_pcs; then
  echo "Power Control did not become ready!"
  cleanup 1
fi

# execute the CT smoke tests
if ! docker compose up --no-deps --no-recreate --exit-code-from smoke smoke; then
  echo "CT smoke tests FAILED!"
  cleanup 1
fi

# execute the CT Tavern tests
if ! docker compose up --no-deps --exit-code-from tavern tavern; then
  echo "CT tavern tests FAILED!"
  cleanup 1
fi

# Cleanup
echo "CT tests PASSED!"
cleanup 0
