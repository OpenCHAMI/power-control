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

CREATE TABLE IF NOT EXISTS transitions (
	"id" UUID PRIMARY KEY,
	"operation" INT NOT NULL,
	"deadline" INT,
	"created" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	"active" TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	"expires" TIMESTAMPTZ,
	"status" VARCHAR(255) NOT NULL
	-- iscompressed omitted. AFAIK this should be handled in app-side representation of the struct
	-- taskcounts omitted. AFAIK this is also built app-side from the tasks
);

CREATE TABLE IF NOT EXISTS transition_tasks (
	"id" UUID PRIMARY KEY,
	"transition_id" UUID NOT NULL,
	"operation" INT NOT NULL,
	"state" INT NOT NULL,
	"xname" VARCHAR(255) NOT NULL,
	-- no idea what these actually look like in practice
	"reservation_key" VARCHAR(255),
	-- no idea what these actually look like in practice
	"deputy_key" VARCHAR(255),
	"status" VARCHAR(255) NOT NULL,
	"status_desc" TEXT,
	"error" TEXT
	-- Unsure if this should cascade. we probably have no reason to keep tasks around if the transition parent is gone?
	-- However, PCS has (and uses) independent delete functions for them, and sometimes (at least in tests) creates
	-- them _before_ their associatied transition, so we can't enforce this as-is.
	--FOREIGN KEY ("transition_id") REFERENCES transitions ("id") ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS transition_locations (
	"id" SERIAL PRIMARY KEY,
	"transition_id" UUID NOT NULL,
	"xname" VARCHAR(255) NOT NULL,
	-- no idea what these actually look like in practice
	"deputy_key" VARCHAR(255),
	-- Locations have no independent CRUD functions and are tied to a specific transition ID, so while they're
	-- sorta separate objects, they become inaccessible without their attached transition.
	FOREIGN KEY ("transition_id") REFERENCES transitions ("id") ON DELETE CASCADE
);

CREATE INDEX idx_transition_locations ON transition_locations (transition_id);

COMMIT;
