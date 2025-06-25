-- MIT License
--
-- Copyright © 2025 Contributors to the OpenCHAMI Project
--
-- Permission is hereby granted, free of charge, to any person obtaining a copy
-- of this software and associated documentation files (the "Software"), to deal
-- in the Software without restriction, including without limitation the rights
-- to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
-- copies of the Software, and to permit persons to whom the Software is
-- furnished to do so, subject to the following conditions:
--
-- The above copyright notice and this permission notice shall be included in all
-- copies or substantial portions of the Software.
--
-- THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
-- IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
-- FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
-- AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
-- LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
-- OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
-- SOFTWARE.

BEGIN;

CREATE TABLE IF NOT EXISTS power_cap_tasks (
	"id" UUID PRIMARY KEY,
	"type" VARCHAR(255) NOT NULL,
	"created" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	"expires" TIMESTAMPTZ,
	"status" VARCHAR(255) NOT NULL,
	"snapshot_parameters" JSON, -- just an array of xnames, but it being embedded in a struct means we can't serialize to array directly
	"patch_parameters" JSON,
	-- components and task_counts are only populated when compressed == true. these are completed tasks that have been
	-- archived. components and task_counts are derived from the operations associated with this task, and summarize
	-- those (deleted) rows.
	"compressed" BOOL,
	"components" JSON,
	"task_counts" JSON

);

CREATE TABLE IF NOT EXISTS power_cap_operations (
	"id" UUID PRIMARY KEY,
	"task_id" UUID NOT NULL,
	"type" VARCHAR(255) NOT NULL,
	"status" VARCHAR(255) NOT NULL,
	"component" JSON,
	FOREIGN KEY ("task_id") REFERENCES power_cap_tasks ("id") ON DELETE CASCADE
);

COMMIT;
