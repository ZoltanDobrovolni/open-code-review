#!/usr/bin/env node

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

"use strict";

const assert = require("assert");

const { forbidden } = require("./check-ocr-local-deps.js");
const { parseArgs } = require("./create-ocr-local-manifest.js");
const { parseInvocation } = require("./run-ocr-local.js");

assert(forbidden.includes("/internal/llm"));
assert(forbidden.includes("github.com/modelcontextprotocol/"));

assert.deepStrictEqual(
    parseArgs(["--binary", "dist/ocr-local.exe", "--version", "v1.2.3", "--output", ".ocr-local/manifest.json"]),
    { binary: "dist/ocr-local.exe", version: "v1.2.3", output: ".ocr-local/manifest.json" },
);

assert.deepStrictEqual(
    parseInvocation(["--manifest", ".ocr-local/manifest.json", "--", "delegate", "preview", "--format", "json"]),
    {
        manifestPath: ".ocr-local/manifest.json",
        commandArgs: ["delegate", "preview", "--format", "json"],
    },
);

assert.throws(
    () => parseInvocation(["--manifest", ".ocr-local/manifest.json"]),
    /usage:/,
);

console.log("All ocr-local script tests passed.");