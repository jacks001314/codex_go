import readline from "node:readline";

const input = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });

function reply(id, result) {
  process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", id, result })}\n`);
}

for await (const line of input) {
  if (!line.trim()) continue;
  const request = JSON.parse(line);
  if (request.method === "initialize") {
    reply(request.id, {
      protocolVersion: "2025-06-18",
      capabilities: { tools: {} },
      serverInfo: { name: "sdktests-fixture", version: "1.0.0" },
    });
  } else if (request.method === "tools/list") {
    reply(request.id, { tools: [
      {
        name: "inspect",
        description: "Return deterministic structured text, image, and audio fixture content.",
        inputSchema: { type: "object", properties: { value: { type: "integer" } }, required: ["value"], additionalProperties: false },
      },
      {
        name: "controlled_failure",
        description: "Return a deterministic MCP business failure for recovery testing.",
        inputSchema: { type: "object", properties: {}, additionalProperties: false },
      },
    ] });
  } else if (request.method === "tools/call") {
    if (request.params?.name === "controlled_failure") {
      reply(request.id, { content: [{ type: "text", text: "MCP_CONTROLLED_FAILURE" }], isError: true });
    } else {
      reply(request.id, {
        content: [
          { type: "text", text: "MCP_TEXT_OK" },
          { type: "image", data: "aW1hZ2U=", mimeType: "image/png", _meta: { "codex/imageDetail": "original" } },
          { type: "audio", data: "YXVkaW8=", mimeType: "audio/wav" },
        ],
        structuredContent: { answer: 42, echoed: request.params?.arguments?.value },
        isError: false,
        _meta: { fixture: true },
      });
    }
  } else if (request.id !== undefined) {
    reply(request.id, {});
  }
}
