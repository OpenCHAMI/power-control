/*
 * MIT License
 *
 * (C) Copyright [2021-2025] Hewlett Packard Enterprise Development LP
 *
 * Permission is hereby granted, free of charge, to any person obtaining a
 * copy of this software and associated documentation files (the "Software"),
 * to deal in the Software without restriction, including without limitation
 * the rights to use, copy, modify, merge, publish, distribute, sublicense,
 * and/or sell copies of the Software, and to permit persons to whom the
 * Software is furnished to do so, subject to the following conditions:
 * The above copyright notice and this permission notice shall be included
 * in all copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
 * THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
 * OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
 * ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
 * OTHER DEALINGS IN THE SOFTWARE.
 *
 */

package main

import (
	"os"

	"github.com/OpenCHAMI/power-control/v2/internal/logger"
	"github.com/OpenCHAMI/power-control/v2/internal/storage"
)

func main() {
	pcs := pcsConfig{
		hsmLockEnabled:    true,
		runControl:        false,
		credCacheDuration: 600,
	}
	postgres := storage.DefaultPostgresConfig()
	var etcd etcdConfig
	var schema schemaConfig
	var oauth2 oauth2Config

	logger.Init()

	rootCommand := createRootCommand(&pcs, &etcd, &postgres, &oauth2)
	// Add the Postgres initialization command
	rootCommand.AddCommand(createPostgresInitCommand(&postgres, &schema))

	if err := rootCommand.Execute(); err != nil {
		os.Exit(1)
	}
}
