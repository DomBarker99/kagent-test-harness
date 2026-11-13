// The loop program: one process per turn.
//
// stdin:  one JSON line, a TurnInput.
// stdout: JSON lines, each a LoopEvent. The last "final" event is the reply.
// stderr: free-form logging; it ends up in the container log.
//
// For now this is a debug terminal, with no model involved: the user's message
// is run as a shell command and its output is the reply. Nothing carries over
// between turns, so `cd` only lasts within one message (`cd /data && ls`).
//
// Run it on its own, no kagent needed:
//   echo '{"contextId":"c1","taskId":"t1","text":"id; pwd"}' | node loop/loop.ts
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";

type TurnInput = {
  contextId: string; // the kagent Session; stable across turns
  taskId: string; // this turn
  text: string; // the user's message
};

type LoopEvent = { type: "final"; text: string };

function emit(event: LoopEvent): void {
  process.stdout.write(JSON.stringify(event) + "\n");
}

const input: TurnInput = JSON.parse(readFileSync(0, "utf8"));
console.error(`turn ${input.taskId} in session ${input.contextId}`);

// The agent loop goes here: call the model, run any tool calls, feed the
// results back, repeat until the model stops asking for tools.
// For now the message itself is the command. The image is Alpine, so `sh` is
// busybox, not bash. The reply only goes out when the command ends, so a
// command that runs too long is killed rather than left to hang the turn.
const TIMEOUT_MS = 30_000;
const REPLY_LIMIT = 200_000; // characters of output kept in the reply

const run = spawnSync("sh", ["-c", input.text], {
  encoding: "utf8",
  timeout: TIMEOUT_MS,
  maxBuffer: 8 << 20,
});
let output = (run.stdout ?? "") + (run.stderr ?? "");
if (output.length > REPLY_LIMIT) {
  output = output.slice(0, REPLY_LIMIT) + `\n[cut at ${REPLY_LIMIT} characters]`;
}
if (output !== "" && !output.endsWith("\n")) output += "\n";

// How the command ended: its exit code, the signal that killed it, or why it never ran.
const status = run.error
  ? `error: ${run.error.message}`
  : run.signal
    ? `signal ${run.signal}`
    : `exit ${run.status}`;
emit({ type: "final", text: `${output}[${status}]` });
