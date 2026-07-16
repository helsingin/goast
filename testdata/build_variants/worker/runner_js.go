package worker

import "syscall/js"

// Run is the JavaScript/Wasm implementation.
func (Runner) Run() {}

// WasmCall proves that alternate-target standard-library imports use the
// configured build context rather than host export data.
func WasmCall(r Runner) js.Value {
	r.Run()
	return js.Undefined()
}
