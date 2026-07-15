// Command locmaf-wasm is a browser build of the LOCMAF reference tooling.
//
// It compiles to js/wasm and exposes three functions on the JS global
// object, each taking file bytes (a Uint8Array) plus an optional init
// segment and returning a JSON string:
//
//	locmafInspect(data, init?, strict?)  auto-detect: align a CMAF file
//	                                     or verify+dump a .locmaf file
//	locmafVerify(data, init?, strict?)   conformance-check a .locmaf file
//	locmafDump(data, init?)              walk a .locmaf file's Objects
//
// All the analysis lives in github.com/Eyevinn/locmaf/conform, the same
// package that backs the locmaf CLI — so the browser checker and the
// command line share one code path and give identical verdicts. This
// shim is only the JS glue; it runs entirely client-side, so the file
// never leaves the browser.
//
//go:build js && wasm

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"syscall/js"

	"github.com/Eyevinn/locmaf"
	"github.com/Eyevinn/locmaf/conform"
	"github.com/Eyevinn/locmaf/internal"
)

func main() {
	js.Global().Set("locmafVersion", js.ValueOf(locmaf.Version))
	js.Global().Set("locmafBuild", js.ValueOf(internal.GetVersion()))
	js.Global().Set("locmafInspect", js.FuncOf(inspect))
	js.Global().Set("locmafVerify", js.FuncOf(verify))
	js.Global().Set("locmafDump", js.FuncOf(dump))
	// Keep the instance alive so the exported functions remain callable.
	select {}
}

// --- JS glue -------------------------------------------------------------

// toBytes copies a JS Uint8Array (or null/undefined) into a Go slice.
func toBytes(v js.Value) []byte {
	if v.IsNull() || v.IsUndefined() {
		return nil
	}
	n := v.Get("length").Int()
	b := make([]byte, n)
	js.CopyBytesToGo(b, v)
	return b
}

func okJSON(v any) string {
	b, err := json.Marshal(map[string]any{"ok": true, "result": v})
	if err != nil {
		return errJSON(err.Error())
	}
	return string(b)
}

func errJSON(msg string) string {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": msg})
	return string(b)
}

// guard runs fn, turning any error or panic (a malformed file must not
// kill the wasm instance) into an error JSON string.
func guard(fn func() (any, error)) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = errJSON(fmt.Sprintf("internal error: %v", r))
		}
	}()
	v, err := fn()
	if err != nil {
		return errJSON(err.Error())
	}
	return okJSON(v)
}

func boolArg(args []js.Value, i int, def bool) bool {
	if len(args) > i && args[i].Type() == js.TypeBoolean {
		return args[i].Bool()
	}
	return def
}

func argAt(args []js.Value, i int) js.Value {
	if len(args) > i {
		return args[i]
	}
	return js.Null()
}

func inspect(_ js.Value, args []js.Value) any {
	data, initBytes, strict := toBytes(args[0]), toBytes(argAt(args, 1)), boolArg(args, 2, true)
	return guard(func() (any, error) { return inspectBytes(data, initBytes, strict) })
}

func verify(_ js.Value, args []js.Value) any {
	data, initBytes, strict := toBytes(args[0]), toBytes(argAt(args, 1)), boolArg(args, 2, true)
	return guard(func() (any, error) { return conform.Verify(data, initBytes, strict) })
}

func dump(_ js.Value, args []js.Value) any {
	data, initBytes := toBytes(args[0]), toBytes(argAt(args, 1))
	return guard(func() (any, error) { return conform.Dump(data, initBytes) })
}

// inspectBytes decides what kind of file it was handed. A fragmented
// CMAF/MP4 file loads cleanly (LoadCMAF); a .locmaf file does not (it is
// length-prefixed LOCMAF framing, not ISOBMFF). CMAF input runs the align
// round-trip; .locmaf input runs verify plus dump.
func inspectBytes(data, initBytes []byte, strict bool) (any, error) {
	lc, err := conform.LoadCMAF(data, initBytes)
	if err == nil {
		rep, _, aerr := conform.Align(lc, false, false)
		if aerr != nil {
			return nil, aerr
		}
		return map[string]any{"mode": "align", "align": rep}, nil
	}
	if errors.Is(err, conform.ErrNoInit) {
		// It is a fragmented CMAF file, but the media segment carries no
		// inline init and none was supplied.
		return nil, fmt.Errorf("fragmented CMAF with no inline init: supply a separate init segment (ftyp+moov)")
	}

	vr, verr := conform.Verify(data, initBytes, strict)
	if verr != nil {
		return nil, fmt.Errorf("not a fragmented CMAF file and not a valid .locmaf file: %w", verr)
	}
	dr, derr := conform.Dump(data, initBytes)
	if derr != nil {
		return nil, derr
	}
	return map[string]any{"mode": "locmaf", "verify": vr, "dump": dr}, nil
}
