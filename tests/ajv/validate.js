#!/usr/bin/env node
// Validates every schema under the given schema directory against its
// examples, in one process. Called by tests/schemas-test.sh; the rules for
// file names are in that script's header.
//
// It builds each validator the way ajv-cli 5.0.0 does for
// `ajv test|validate --spec=draft2020 --strict=true -s <schema> -r <others>`:
// an Ajv2020 instance with strict on, the draft-06 meta schema added, and
// every other schema added as a reference. A fresh instance per schema keeps
// one schema's compile error from reaching the others.
//
// Usage:
//   node validate.js <schema-dir> <node_modules-dir>
//     [--live <schema-name> <data-file> <label>]... [--note <fail|skip> <text-file>]...
// The second argument names where ajv is installed. Each --live also
// validates one data file against one schema, as `ajv validate` does without
// --all-errors, and reports it under <label>. Each --note prints a line the
// caller wrote, after the examples; a fail note fails the run.
//
// The result goes to process.exitCode, never process.exit, so output on a
// pipe is written in full before Node.js exits.
"use strict";

const fs = require("fs");
const path = require("path");

const usage = "usage: node validate.js <schema-dir> <node_modules-dir> " +
  "[--live <schema-name> <data-file> <label>]... [--note <fail|skip> <text-file>]...";
const args = process.argv.slice(2);
const [schemaDir, modulesDir] = args;
const lives = [];
const notes = [];
let badArgs = false;
for (let i = 2; i < args.length; ) {
  if (args[i] === "--live" && i + 3 < args.length) {
    lives.push({ name: args[i + 1], file: args[i + 2], label: args[i + 3] });
    i += 4;
  } else if (args[i] === "--note" && i + 2 < args.length && ["fail", "skip"].includes(args[i + 1])) {
    notes.push({ fail: args[i + 1] === "fail", file: args[i + 2] });
    i += 3;
  } else {
    badArgs = true;
    break;
  }
}
if (!schemaDir || !modulesDir || badArgs) {
  console.error(usage);
  process.exit(2);
}
const load = (id) => require(require.resolve(id, { paths: [modulesDir] }));
const Ajv2020 = load("ajv/dist/2020").default;
const draft6 = load("ajv/lib/refs/json-schema-draft-06.json");

const examples = path.join(schemaDir, "examples");
const readJSON = (file) => JSON.parse(fs.readFileSync(file, "utf8"));
const indent = (text) => text.split("\n").map((l) => "     " + l).join("\n");
const sorted = (dir, keep) => fs.readdirSync(dir).filter(keep).sort();

let schemaFiles;
let exampleFiles;
try {
  schemaFiles = sorted(schemaDir, (f) => f.endsWith(".schema.json"));
  exampleFiles = sorted(examples, (f) => f.endsWith(".json"));
} catch (err) {
  console.log(`FAIL cannot read ${err.path || schemaDir}: no schema or examples directory`);
  console.log("FAILED");
  process.exitCode = 1;
  return;
}

// Returns a validate function for schema `name`, or throws its compile error.
function compile(name, allErrors) {
  const ajv = new Ajv2020({ strict: true, allErrors });
  ajv.addMetaSchema(draft6);
  for (const other of schemaFiles) {
    if (other !== `${name}.schema.json`) ajv.addSchema(readJSON(path.join(schemaDir, other)));
  }
  return ajv.compile(readJSON(path.join(schemaDir, `${name}.schema.json`)));
}

let fail = false;
let checked = 0;
for (const file of schemaFiles) {
  const name = file.slice(0, -".schema.json".length);
  if (name === "finding") continue;

  const valid = `${name}.valid.json`;
  const valids = exampleFiles.filter((f) => f === valid || (f.startsWith(`${name}.valid.`) && f !== valid));
  const invalids = exampleFiles.filter((f) => f.startsWith(`${name}.invalid.`));
  if (!exampleFiles.includes(valid) || invalids.length === 0) {
    console.log(`FAIL ${name}: needs ${name}.valid.json and at least one ${name}.invalid.<keyword>.json`);
    fail = true;
    continue;
  }

  // Valid examples: the first-error mode of `ajv test --valid`.
  let first;
  try {
    first = compile(name, false);
  } catch (err) {
    first = err;
  }
  for (const f of valids) {
    let out;
    if (first instanceof Error) {
      out = `schema ${file} is invalid\nerror: ${first.message}`;
    } else {
      try {
        if (first(readJSON(path.join(examples, f)))) {
          console.log(`ok   ${f} passes`);
          continue;
        }
        out = JSON.stringify(first.errors, null, "  ");
      } catch (err) {
        out = `error: ${err.message}`;
      }
    }
    console.log(`FAIL ${f} does not pass:\n${indent(out)}`);
    fail = true;
  }

  // Invalid examples: the all-errors mode of `ajv validate --all-errors`.
  // Each must fail with an error whose keyword is the third dot field of its
  // name, so a failure names the file and its expected keyword.
  let all;
  try {
    all = compile(name, true);
  } catch (err) {
    all = err;
  }
  for (const f of invalids) {
    const base = f.slice(0, -".json".length);
    const keyword = base.split(".")[2];
    if (!keyword) {
      console.log(`FAIL ${f}: needs a keyword in its name, ${name}.invalid.<keyword>.json`);
      fail = true;
      continue;
    }
    let out;
    if (all instanceof Error) {
      out = `schema ${file} is invalid\nerror: ${all.message}`;
    } else {
      try {
        if (all(readJSON(path.join(examples, f)))) {
          console.log(`FAIL ${f} passes but must fail`);
          fail = true;
          continue;
        }
        if (all.errors.some((e) => e.keyword === keyword)) {
          console.log(`ok   ${f} fails on ${keyword}`);
          continue;
        }
        out = JSON.stringify(all.errors, null, "  ");
      } catch (err) {
        out = `error: ${err.message}`;
      }
    }
    console.log(`FAIL ${f} fails, but not on ${keyword}:\n${indent(out)}`);
    fail = true;
  }
  checked++;
}

if (checked === 0) console.log("FAIL no schemas found");

for (const live of lives) {
  let out;
  try {
    const validate = compile(live.name, false);
    if (validate(readJSON(live.file))) {
      console.log(`ok   ${live.label} passes`);
    } else {
      out = JSON.stringify(validate.errors, null, "  ");
    }
  } catch (err) {
    out = `error: ${err.message}`;
  }
  if (out !== undefined) {
    console.log(`FAIL ${live.label} does not pass:\n${indent(out)}`);
    fail = true;
  }
}
for (const note of notes) {
  process.stdout.write(fs.readFileSync(note.file, "utf8"));
  if (note.fail) fail = true;
}

if (checked === 0 || fail) {
  console.log("FAILED");
  process.exitCode = 1;
} else {
  console.log(`PASS ${checked} schemas`);
}
