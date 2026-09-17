#!/usr/bin/env node

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

"use strict";

const { spawnSync } = require("child_process");

const forbidden = [
    "/internal/agent",
    "/internal/llm",
    "/internal/llmloop",
    "/internal/mcp",
    "/internal/scan",
    "/internal/session",
    "/internal/telemetry",
    "github.com/anthropics/",
    "github.com/aws/",
    "github.com/modelcontextprotocol/",
    "github.com/openai/",
    "go.opentelemetry.io/",
];

function main() {
    const result = spawnSync("go", ["list", "-deps", "./cmd/ocr-local"], {
        encoding: "utf8",
        shell: false,
    });
    if (result.error || result.status !== 0) {
        throw new Error(`go list failed: ${result.error?.message || result.stderr || result.status}`);
    }
    const deps = result.stdout.split(/\r?\n/).filter(Boolean);
    const violations = deps.filter((dep) => forbidden.some((item) => dep.includes(item)));
    if (violations.length > 0) {
        throw new Error(`ocr-local imports prohibited packages:\n${violations.join("\n")}`);
    }
    process.stdout.write("ocr-local dependency policy passed\n");
}

if (require.main === module) {
    try {
        main();
    } catch (error) {
        console.error(`error: ${error.message}`);
        process.exit(1);
    }
}

module.exports = { forbidden };