// The loop program: one process per turn.
//
// stdin:  one JSON line, a TurnInput.
// stdout: JSON lines, each a LoopEvent. The last "final" event is the reply.
// stderr: free-form logging; it ends up in the container log.
//
// Run it on its own, no kagent needed:
//   echo '{"contextId":"c1","taskId":"t1","text":"hello"}' | node loop/loop.ts
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
// For now the turn only echoes, which proves the pipe end to end.
emit({ type: "final", text: `loop.ts received: ${input.text}` });
