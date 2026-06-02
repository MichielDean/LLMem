import { tool } from "@opencode-ai/plugin";
import { runLlmem } from "./lib/_llmem";

/**
 * Add a new memory to llmem.
 *
 * Invokes `llmem add --type <TYPE> --content <CONTENT> [--source SOURCE] [--confidence FLOAT]`
 * and returns the added memory ID and type. On invalid type or other error,
 * returns a string starting with "Error:".
 */
export default tool({
  name: "llmem-add",
  description:
    "Add a new memory to llmem. Returns the memory ID and type on success. Requires no prior initialization — the CLI handles store creation.",
  args: {
    type: tool.schema.string().describe(
      "Memory type (fact, decision, preference, event, project_state, procedure, conversation)"
    ),
    content: tool.schema.string().describe(
      "Memory content text"
    ),
    source: tool.schema.string().optional().describe(
      "Source of memory (default: manual)"
    ),
    confidence: tool.schema.number().min(0).max(1).optional().describe(
      "Confidence 0-1 (default: 0.8)"
    ),
  },
  execute: async (args, context) => {
    const cmdArgs: string[] = [
      "add",
      "--type", args.type,
      "--content", args.content,
    ];

    if (args.source) {
      cmdArgs.push("--source", args.source);
    }
    if (args.confidence !== undefined) {
      cmdArgs.push("--confidence", String(args.confidence));
    }

    const result = await runLlmem(cmdArgs, {
      worktree: context?.worktree,
    });

    if (result.exitCode !== 0) {
      return result.stdout;
    }

    return result.stdout;
  },
});