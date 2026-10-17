package metrics

// ExecServerClientRequestCountMetric counts caller-side exec-server RPC
// attempts, including local admission and transport failures. It is shared by
// telemetry and the execserver package so the leaf client can record it
// without importing the larger telemetry graph.
const ExecServerClientRequestCountMetric = "exec_server_client_requests_total"
