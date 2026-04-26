// Package types holds pure data types shared across worker layers.
// Nothing here imports from internal/ — this is the bottom of the
// layer stack. Types here may not carry behavior beyond pure
// value-level helpers (String, Equal, etc.).
package types
