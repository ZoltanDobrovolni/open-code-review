#!/usr/bin/env node

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

"use strict";

const crypto = require("crypto");
const fs = require("fs");
const path = require("path");
const { spawn, spawnSync } = require("child_process");

function parseInvocation(argv) {
    const separator = argv.indexOf("--");
    if (separator < 0 || separator === argv.length - 1) {
        throw new Error("usage: node scripts/run-ocr-local.js --manifest <path> -- <ocr-local args>");
    }
    let manifestPath = "";
    for (let i = 0; i < separator; i += 1) {
        if (argv[i] !== "--manifest" || !argv[i + 1] || argv[i + 1] === "--") {
            throw new Error("only --manifest <path> is accepted before --");
        }
        manifestPath = argv[++i];
    }
    if (!manifestPath) throw new Error("--manifest is required");
    return { manifestPath, commandArgs: argv.slice(separator + 1) };
}

function loadManifest(manifestPath) {
    const absoluteManifest = path.resolve(manifestPath);
    const manifest = JSON.parse(fs.readFileSync(absoluteManifest, "utf8"));
    if (typeof manifest.binary !== "string" || typeof manifest.version !== "string") {
        throw new Error("manifest must contain binary and version strings");
    }
    if (!/^[a-f0-9]{64}$/i.test(manifest.sha256 || "")) {
        throw new Error("manifest sha256 must be a 64-character hexadecimal digest");
    }
    return {
        manifest,
        binary: path.resolve(path.dirname(absoluteManifest), manifest.binary),
    };
}

function verifyBinary(binary, manifest) {
    if (!fs.statSync(binary).isFile()) throw new Error(`binary is not a file: ${binary}`);
    const actualSha256 = crypto.createHash("sha256").update(fs.readFileSync(binary)).digest("hex");
    if (actualSha256.toLowerCase() !== manifest.sha256.toLowerCase()) {
        throw new Error(`binary checksum mismatch: got ${actualSha256}, want ${manifest.sha256}`);
    }
    const version = spawnSync(binary, ["--version"], { encoding: "utf8", shell: false });
    if (version.error || version.status !== 0) {
        throw new Error(`failed to verify binary version: ${version.error?.message || version.stderr || version.status}`);
    }
    const reported = version.stdout.trim().split(/\r?\n/, 1)[0];
    const expected = `ocr-local ${manifest.version}`;
    if (reported !== expected) throw new Error(`binary version mismatch: got ${reported}, want ${expected}`);
}

function main() {
    const invocation = parseInvocation(process.argv.slice(2));
    const loaded = loadManifest(invocation.manifestPath);
    verifyBinary(loaded.binary, loaded.manifest);
    const child = spawn(loaded.binary, invocation.commandArgs, {
        env: process.env,
        shell: false,
        stdio: "inherit",
    });
    child.on("error", (error) => {
        console.error(`error: failed to start ocr-local: ${error.message}`);
        process.exitCode = 1;
    });
    child.on("close", (code, signal) => {
        if (signal) process.exitCode = 1;
        else process.exitCode = code ?? 1;
    });
}

if (require.main === module) {
    try {
        main();
    } catch (error) {
        console.error(`error: ${error.message}`);
        process.exit(1);
    }
}

module.exports = { loadManifest, parseInvocation, verifyBinary };