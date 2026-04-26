// Package backendhttp is the worker-node's HTTP client for OpenAI-
// compatible inference backends (vLLM, llama.cpp server, TEI, SDXL
// servers, whisper, etc.). Kept behind a small interface so modules
// can be unit-tested against a fake without spinning up real backends.
//
// The interface has two methods — one for non-streaming request/
// response JSON and one for SSE-streaming responses. Both accept a
// pre-built target URL plus the request body bytes (already validated
// upstream by the module's ExtractModel / EstimateWorkUnits pass).
package backendhttp
