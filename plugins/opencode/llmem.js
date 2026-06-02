/**
 * LLMem OpenCode Plugin — session lifecycle hooks for memory injection.
 *
 * This plugin handles two things automatically:
 * 1. session.created — injects memory context and stats into the session
 * 2. experimental.session.compacting — preserves key memories across compaction
 *
 * All behavioral instructions (when to search, when to add, when to consolidate)
 * live in the llmem SKILL.md and are loaded on-demand by the agent.
 * This plugin only handles the automatic lifecycle hooks.
 *
 * Installation: Add "opencode-llmem" to the plugin array in opencode.json,
 * or copy this file to ~/.config/opencode/plugins/llmem.js
 *
 * Requires: llmem CLI on PATH (Go binary at ~/.local/bin/llmem)
 */

const child_process = require("child_process");
const path = require("path");
const fs = require("fs");
const os = require("os");

const LLMEM = "llmem";
const TIMEOUT_MS = 60000;
const INJECT_TAG = "## LLMem Context";
const STATS_TAG = "## LLMem Stats";

function run(args, timeout) {
  try {
    return child_process.execFileSync(LLMEM, args, {
      encoding: "utf8",
      timeout: timeout || TIMEOUT_MS,
    }).trim();
  } catch (e) {
    return null;
  }
}

function runAsync(args, timeout) {
  try {
    const proc = child_process.spawn(LLMEM, args, {
      stdio: "pipe",
      detached: true,
    });
    proc.unref();
  } catch {}
}

function log(client, level, message) {
  if (!client || !client.app || !client.app.log) return;
  try {
    client.app.log({
      body: { service: "llmem", level, message },
    });
  } catch {}
}

const LLMemPlugin = async function ({ client, $, directory, worktree }) {
  return {
    event: async function ({ event }) {
      if (event.type === "session.created") {
        const sessionId =
          event.properties &&
          (event.properties.sessionId || event.properties.id);

        const stats = run(["stats"]);
        if (stats) {
          log(client, "info", STATS_TAG + "\n" + stats);
        }

        const context = run([
          "search",
          "recent decisions important facts preferences",
          "--limit",
          "5",
        ]);
        if (context) {
          log(client, "info", INJECT_TAG + "\n" + context);
        }
      }
    },

    "experimental.session.compacting": async function (input, output) {
      const result = run([
        "search",
        "recent decisions important facts preferences procedures",
        "--limit",
        "15",
      ]);
      if (result) {
        output.context.push(INJECT_TAG + "\n\n" + result);
      }
    },
  };
};

module.exports = { LLMemPlugin };
if (typeof window === "undefined" || typeof globalThis !== "undefined") {
  try {
    globalThis.LLMemPlugin = LLMemPlugin;
  } catch {}
}