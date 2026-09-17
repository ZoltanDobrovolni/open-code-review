#!/usr/bin/env node

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

"use strict";

const crypto = require("crypto");
const fs = require("fs");
const path = require("path");
const { spawnSync } = require("child_process");

function usage() {
    throw new Error(
        "usage: node scripts/create-ocr-local-manifest.js --binary <path> --version <version> --output <path>"
    );
}

function parseArgs(argv) {
    const values = {};
    for (let i = 0; i < argv.length; i += 1) {
        const flag = argv[i];
        if (!["--binary", "--version", "--output"].includes(flag)) usage();
        const value = argv[++i];
        if (!value || value.startsWith("--")) usage();
        values[flag.slice(2)] = value;
    }
    if (!values.binary || !values.version || !values.output) usage();
    return values;
}

function binaryVersion(binary) {
    const result = spawnSync(binary, ["--version"], { encoding: "utf8", shell: false });
    if (result.error || result.status !== 0) {
        throw new Error(`failed to read binary version: ${result.error?.message || result.stderr || result.status}`);
    }
    return result.stdout.trim().split(/\r?\n/, 1)[0];
}

function main() {
    const args = parseArgs(process.argv.slice(2));
    const binary = path.resolve(args.binary);
    const output = path.resolve(args.output);
    if (!fs.statSync(binary).isFile()) throw new Error(`binary is not a file: ${binary}`);

    const reportedVersion = binaryVersion(binary);
    const expectedVersion = `ocr-local ${args.version}`;
    if (reportedVersion !== expectedVersion) {
        throw new Error(`binary version mismatch: got ${reportedVersion}, want ${expectedVersion}`);
    }

    const sha256 = crypto.createHash("sha256").update(fs.readFileSync(binary)).digest("hex");
    const relativeBinary = path.relative(path.dirname(output), binary).split(path.sep).join("/");
    fs.mkdirSync(path.dirname(output), { recursive: true });
    fs.writeFileSync(
        output,
        `${JSON.stringify({ binary: relativeBinary, version: args.version, sha256 }, null, 2)}\n`,
        { mode: 0o600 },
    );
    process.stdout.write(`created ${output}\n`);
}

if (require.main === module) {
    try {
        main();
    } catch (error) {
        console.error(`error: ${error.message}`);
        process.exit(1);
    }
}

module.exports = { binaryVersion, parseArgs };