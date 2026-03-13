// Oracle harness for comparison testing Go vs TypeScript implementations.
// Reads JSONL commands from stdin, calls the TS reference implementations,
// writes JSONL responses to stdout.
//
// Protocol:
//   Request:  {"cmd": "transformMessages", "messages": [...], "model": {...}}
//   Response: {"result": [...]}
//
//   Request:  {"cmd": "calculateCost", "model": {...}, "usage": {...}}
//   Response: {"result": {...}}
//
//   Request:  {"cmd": "isContextOverflow", "message": {...}, "contextWindow": 128000}
//   Response: {"result": true|false}

import { createInterface } from "node:readline";
import { transformMessages } from "@mariozechner/pi-ai/dist/providers/transform-messages.js";
import { calculateCost, isContextOverflow } from "@mariozechner/pi-ai";

const rl = createInterface({ input: process.stdin });

for await (const line of rl) {
  if (!line.trim()) continue;

  try {
    const req = JSON.parse(line);
    let result;

    switch (req.cmd) {
      case "transformMessages":
        result = transformMessages(req.messages, req.model, null);
        break;
      case "calculateCost":
        result = calculateCost(req.model, req.usage);
        break;
      case "isContextOverflow":
        result = isContextOverflow(req.message, req.contextWindow);
        break;
      default:
        process.stdout.write(JSON.stringify({ error: `unknown command: ${req.cmd}` }) + "\n");
        continue;
    }

    process.stdout.write(JSON.stringify({ result }) + "\n");
  } catch (err) {
    process.stdout.write(JSON.stringify({ error: err.message }) + "\n");
  }
}
