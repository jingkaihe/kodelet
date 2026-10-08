package codemode

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/pkg/errors"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// See runtime_wasm_provenance.md for the pinned source, artifact digest, and license.
//
//go:generate npm ci --ignore-scripts
//go:generate npm run generate
//go:embed runtime_quickjs.wasm
var runtimeWASM []byte

//go:embed runtime_prelude.js
var runtimePrelude string

// Only immutable compiled code is shared. Every invocation has separate guest
// memory, runtime globals, callbacks, pending promises, and cancellation context.
var runtimeCompileCache = wazero.NewCompilationCache()

type runtimeRejection struct {
	promise uint64
	reason  uint64
}

type runtimeVM struct {
	ctx         context.Context
	runtime     wazero.Runtime
	module      api.Module
	bridge      *runtimeBridge
	limits      runtimeLimits
	settle      uint64
	serialize   uint64
	formatError uint64
	undefined   uint64
	scratch     uint64
	fatal       error
	rejections  map[uint32]runtimeRejection
}

func newRuntimeVM(ctx context.Context, bridge *runtimeBridge, limits runtimeLimits) (_ *runtimeVM, err error) {
	config := wazero.NewRuntimeConfig().
		WithCompilationCache(runtimeCompileCache).
		WithMemoryLimitPages(limits.memoryBytes / 65536).
		WithCloseOnContextDone(true)
	vm := &runtimeVM{
		ctx: ctx, runtime: wazero.NewRuntimeWithConfig(ctx, config),
		bridge: bridge, limits: limits, rejections: make(map[uint32]runtimeRejection),
	}
	defer func() {
		if err != nil {
			vm.close()
		}
	}()
	// The default WASI module config has no environment, argv, preopened
	// directories, stdin, stdout/stderr writers, sockets, or host randomness.
	if _, err = wasi_snapshot_preview1.Instantiate(ctx, vm.runtime); err != nil {
		return nil, err
	}
	_, err = vm.runtime.NewHostModuleBuilder("env").
		NewFunctionBuilder().WithFunc(vm.hostCall).Export("host_call").
		NewFunctionBuilder().WithFunc(func() uint32 {
		if ctx.Err() != nil || vm.fatal != nil {
			return 1
		}
		return 0
	}).Export("host_interrupt").
		NewFunctionBuilder().WithFunc(vm.promiseRejection).Export("host_promise_rejection").
		NewFunctionBuilder().WithFunc(func(uint32, uint32) uint32 { return 0 }).Export("host_module_normalize").
		NewFunctionBuilder().WithFunc(func(uint32, uint32) uint32 { return 0 }).Export("host_module_load").
		NewFunctionBuilder().WithFunc(func(uint32, uint32) uint32 { return 0 }).Export("host_get_timezone_offset").
		Instantiate(ctx)
	if err != nil {
		return nil, err
	}
	compiled, err := vm.runtime.CompileModule(ctx, runtimeWASM)
	if err != nil {
		return nil, err
	}
	vm.module, err = vm.runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithStartFunctions("_initialize"))
	if err != nil {
		return nil, err
	}
	if _, err = vm.call("qjs_init"); err != nil {
		return nil, err
	}
	// Leave linear-memory headroom for C marshalling and the WASM stack.
	if _, err = vm.call("qjs_set_memory_limit", uint64(limits.memoryBytes)*3/4); err != nil {
		return nil, err
	}
	if _, err = vm.call("qjs_set_max_stack_size", 512<<10); err != nil {
		return nil, err
	}
	if _, err = vm.call("qjs_set_interrupt_handler", 1); err != nil {
		return nil, err
	}
	if vm.scratch, err = vm.call("wasm_malloc", 4); err != nil || vm.scratch == 0 {
		return nil, errors.New("allocate QuickJS string-length buffer")
	}
	if vm.undefined, err = vm.call("qjs_get_undefined"); err != nil {
		return nil, err
	}
	name, err := vm.writeBytes([]byte("__kodelet_host"))
	if err != nil {
		return nil, err
	}
	defer vm.freeBuffer(name)
	host, err := vm.call("qjs_new_host_function", name, uint64(len("__kodelet_host")), 2)
	if err != nil {
		return nil, err
	}
	defer vm.freeValue(host)
	global, err := vm.call("qjs_get_global")
	if err != nil {
		return nil, err
	}
	defer vm.freeValue(global)
	if _, err = vm.call("qjs_set_prop_string", global, name, host); err != nil {
		return nil, err
	}
	prelude, err := vm.eval(runtimePrelude, "code_runtime.js")
	if err != nil {
		return nil, err
	}
	defer vm.freeValue(prelude)
	if vm.settle, err = vm.property(prelude, "settle"); err != nil {
		return nil, err
	}
	if vm.serialize, err = vm.property(prelude, "serialize"); err != nil {
		return nil, err
	}
	if vm.formatError, err = vm.property(prelude, "formatError"); err != nil {
		return nil, err
	}
	if _, err = vm.call("qjs_set_promise_rejection_handler", 1); err != nil {
		return nil, err
	}
	return vm, nil
}

func (vm *runtimeVM) close() { _ = vm.runtime.Close(context.Background()) }

func (vm *runtimeVM) call(name string, args ...uint64) (uint64, error) {
	if vm.ctx.Err() != nil {
		return 0, vm.ctx.Err()
	}
	fn := vm.module.ExportedFunction(name)
	if fn == nil {
		return 0, errors.Errorf("QuickJS export %q is unavailable", name)
	}
	values, err := fn.Call(vm.ctx, args...)
	if err != nil {
		if vm.ctx.Err() != nil {
			return 0, vm.ctx.Err()
		}
		return 0, errors.Wrapf(err, "QuickJS %s", name)
	}
	if len(values) == 0 {
		return 0, nil
	}
	return values[0], nil
}

func (vm *runtimeVM) freeValue(value uint64) {
	if value != 0 {
		_, _ = vm.call("qjs_free_value", value)
	}
}

func (vm *runtimeVM) freeBuffer(pointer uint64) {
	if pointer != 0 {
		_, _ = vm.call("wasm_free", pointer)
	}
}

func (vm *runtimeVM) writeBytes(value []byte) (uint64, error) {
	pointer, err := vm.call("wasm_malloc", uint64(len(value)+1))
	if err != nil {
		return 0, err
	}
	if pointer == 0 {
		return 0, &Error{Kind: "limit", Message: "QuickJS memory limit exceeded"}
	}
	if !vm.module.Memory().Write(uint32(pointer), value) || !vm.module.Memory().WriteByte(uint32(pointer)+uint32(len(value)), 0) {
		return 0, errors.New("QuickJS memory write is outside guest memory")
	}
	return pointer, nil
}

func (vm *runtimeVM) newString(value []byte) (uint64, error) {
	ptr, err := vm.writeBytes(value)
	if err != nil {
		return 0, err
	}
	defer vm.freeBuffer(ptr)
	return vm.call("qjs_new_string", ptr, uint64(len(value)))
}

func (vm *runtimeVM) readString(value uint64, limit int) ([]byte, error) {
	isString, err := vm.call("qjs_is_string", value)
	if err != nil {
		return nil, err
	}
	if isString == 0 {
		return nil, errors.New("QuickJS bridge expected a primitive string")
	}
	pointer, err := vm.call("qjs_get_string_len", value, vm.scratch)
	if err != nil {
		return nil, err
	}
	if pointer == 0 {
		return nil, errors.New("QuickJS string allocation failed")
	}
	defer func() { _, _ = vm.call("qjs_free_cstring", pointer) }()
	length, ok := vm.module.Memory().ReadUint32Le(uint32(vm.scratch))
	if !ok || uint64(length) > uint64(limit) {
		return nil, &Error{Kind: "limit", Message: "bridge string exceeds the byte limit"}
	}
	data, ok := vm.module.Memory().Read(uint32(pointer), length)
	if !ok {
		return nil, errors.New("QuickJS string is outside guest memory")
	}
	// Never retain a borrowed linear-memory slice beyond a host call.
	return append([]byte(nil), data...), nil
}

func (vm *runtimeVM) property(object uint64, name string) (uint64, error) {
	pointer, err := vm.writeBytes([]byte(name))
	if err != nil {
		return 0, err
	}
	defer vm.freeBuffer(pointer)
	return vm.call("qjs_get_prop_string", object, pointer)
}

func (vm *runtimeVM) eval(code, filename string) (uint64, error) {
	source, err := vm.writeBytes([]byte(code))
	if err != nil {
		return 0, err
	}
	defer vm.freeBuffer(source)
	file, err := vm.writeBytes([]byte(filename))
	if err != nil {
		return 0, err
	}
	defer vm.freeBuffer(file)
	value, err := vm.call("qjs_eval", source, uint64(len(code)), file, 0)
	if err != nil {
		return 0, err
	}
	if err := vm.checkException(value); err != nil {
		vm.freeValue(value)
		return 0, err
	}
	return value, nil
}

func (vm *runtimeVM) invoke(function, argument uint64) (uint64, error) {
	pointer, err := vm.call("wasm_malloc", 4)
	if err != nil || pointer == 0 {
		return 0, errors.New("allocate QuickJS argument vector")
	}
	defer vm.freeBuffer(pointer)
	vm.module.Memory().WriteUint32Le(uint32(pointer), uint32(argument))
	value, err := vm.call("qjs_call", function, vm.undefined, 1, pointer)
	if err != nil {
		return 0, err
	}
	if err := vm.checkException(value); err != nil {
		vm.freeValue(value)
		return 0, err
	}
	return value, nil
}

func (vm *runtimeVM) checkException(value uint64) error {
	if vm.fatal != nil {
		return vm.fatal
	}
	if value == 0 {
		return &Error{Kind: "limit", Message: "QuickJS allocation failed"}
	}
	exception, err := vm.call("qjs_is_exception", value)
	if err != nil || exception == 0 {
		return err
	}
	reason, err := vm.call("qjs_get_exception")
	if err != nil {
		return err
	}
	defer vm.freeValue(reason)
	return vm.reasonError(reason)
}

func (vm *runtimeVM) reasonError(reason uint64) error {
	if vm.ctx.Err() != nil {
		return vm.ctx.Err()
	}
	if vm.fatal != nil {
		return vm.fatal
	}
	if vm.formatError != 0 {
		// Don't use invoke/checkException here: a malicious error getter or an
		// exhausted heap can itself throw while formatting the original failure.
		pointer, err := vm.call("wasm_malloc", 4)
		if err == nil && pointer != 0 {
			defer vm.freeBuffer(pointer)
			vm.module.Memory().WriteUint32Le(uint32(pointer), uint32(reason))
			value, callErr := vm.call("qjs_call", vm.formatError, vm.undefined, 1, pointer)
			if callErr == nil && value != 0 {
				defer vm.freeValue(value)
				if data, readErr := vm.readString(value, vm.limits.outputBytes); readErr == nil {
					var failure Error
					if json.Unmarshal(data, &failure) == nil && failure.Message != "" {
						if strings.Contains(failure.Message, "out of memory") {
							failure.Kind = "limit"
						}
						return &failure
					}
				}
			}
		}
	}
	if vm.ctx.Err() != nil {
		return vm.ctx.Err()
	}
	return &Error{Kind: "script_error", Message: "JavaScript execution failed (error unavailable or memory exhausted)"}
}

// hostCall performs only native-value marshalling while QuickJS is on-stack.
// Primitive string conversion cannot execute guest JavaScript. No JS callback,
// job execution, or tool work is re-entered from this synchronous WASM import.
func (vm *runtimeVM) hostCall(_ context.Context, module api.Module, _ uint32, _ uint32, _ uint32, argc uint32, argv uint32) uint32 {
	var failure error
	if argc != 2 {
		failure = &Error{Kind: "invalid_input", Message: "host bridge expects an operation and JSON payload"}
	} else {
		operationPointer, ok1 := module.Memory().ReadUint32Le(argv)
		payloadPointer, ok2 := module.Memory().ReadUint32Le(argv + 4)
		if !ok1 || !ok2 {
			failure = errors.New("invalid host argument vector")
		} else {
			operation, err := vm.readString(uint64(operationPointer), 16)
			if err != nil {
				failure = err
			} else {
				data, err := vm.readString(uint64(payloadPointer), vm.limits.requestBytes)
				if err != nil {
					failure = err
				} else {
					switch string(operation) {
					case "submit":
						failure = vm.bridge.submit(data)
					case "emit":
						failure = vm.bridge.emit(data)
					case "emit.image":
						failure = vm.bridge.emitMedia("image", data)
					case "emit.artifact":
						failure = vm.bridge.emitMedia("artifact", data)
					default:
						failure = &Error{Kind: "invalid_input", Message: "unknown bridge operation"}
					}
				}
			}
		}
	}
	var value uint64
	var err error
	if failure != nil {
		value, err = vm.newString(marshalRuntimeError(failure))
	} else {
		value, err = vm.call("qjs_get_undefined")
	}
	if err != nil {
		vm.fatal = err
		return 0
	}
	return uint32(value)
}

func (vm *runtimeVM) promiseRejection(promise, reason, handled uint32) {
	identity, err := vm.call("qjs_get_value_ptr", uint64(promise))
	if err != nil {
		vm.fatal = err
		return
	}
	if existing, ok := vm.rejections[uint32(identity)]; ok {
		vm.freeValue(existing.promise)
		vm.freeValue(existing.reason)
		delete(vm.rejections, uint32(identity))
	}
	if handled != 0 {
		vm.freeValue(uint64(promise))
		vm.freeValue(uint64(reason))
		return
	}
	if len(vm.rejections) >= vm.limits.unhandledCount {
		vm.freeValue(uint64(promise))
		vm.freeValue(uint64(reason))
		vm.fatal = &Error{Kind: "limit", Message: "too many unhandled promise rejections"}
		return
	}
	vm.rejections[uint32(identity)] = runtimeRejection{promise: uint64(promise), reason: uint64(reason)}
}

func (vm *runtimeVM) execute(code string) error {
	// Put the wrapper prefix on the first source line, not an extra line, so
	// guest stack traces retain the submitted line numbers. The trailing newline
	// prevents a final line comment from consuming the closing wrapper.
	root, err := vm.eval("(async function () {"+code+"\n})()", "code_execute.js")
	if err != nil {
		return err
	}
	defer vm.freeValue(root)
	if _, err := vm.call("qjs_promise_mark_as_handled", root); err != nil {
		return err
	}
	settled := false
	for {
		if vm.ctx.Err() != nil {
			return vm.ctx.Err()
		}
		if vm.fatal != nil {
			return vm.fatal
		}
		for range 64 {
			job, err := vm.call("qjs_execute_pending_job")
			if err != nil {
				return err
			}
			if int32(job) < 0 {
				reason, err := vm.call("qjs_get_exception")
				if err != nil {
					return err
				}
				failure := vm.reasonError(reason)
				vm.freeValue(reason)
				return failure
			}
			if job == 0 {
				break
			}
		}
		state, err := vm.call("qjs_promise_state", root)
		if err != nil {
			return err
		}
		if state != 0 && !settled {
			settled = true
			vm.bridge.closed = true
			value, err := vm.call("qjs_promise_result", root)
			if err != nil {
				return err
			}
			if state == 2 {
				failure := vm.reasonError(value)
				vm.freeValue(value)
				return failure
			}
			if err := vm.emitValue(value); err != nil {
				vm.freeValue(value)
				return err
			}
			vm.freeValue(value)
		}
		jobsPending, err := vm.call("qjs_is_job_pending")
		if err != nil {
			return err
		}
		if jobsPending == 0 {
			for _, rejection := range vm.rejections {
				return vm.reasonError(rejection.reason)
			}
			if len(vm.bridge.pending) == 0 {
				if settled {
					return nil
				}
				return &Error{Kind: "script_error", Message: "script awaits a promise with no pending host work"}
			}
		}
		// One completion per job batch gives both JavaScript and host work fair
		// progress even when guest microtasks continuously enqueue more jobs.
		select {
		case <-vm.ctx.Done():
			return vm.ctx.Err()
		case completion := <-vm.bridge.completions:
			if err := vm.complete(completion); err != nil {
				return err
			}
		default:
			if jobsPending != 0 {
				continue
			}
			select {
			case <-vm.ctx.Done():
				return vm.ctx.Err()
			case completion := <-vm.bridge.completions:
				if err := vm.complete(completion); err != nil {
					return err
				}
			}
		}
	}
}

func (vm *runtimeVM) emitValue(value uint64) error {
	undefined, err := vm.call("qjs_is_undefined", value)
	if err != nil || undefined != 0 {
		return err
	}
	serialized, err := vm.invoke(vm.serialize, value)
	if err != nil {
		return err
	}
	defer vm.freeValue(serialized)
	data, err := vm.readString(serialized, vm.limits.outputBytes)
	if err != nil {
		return err
	}
	return vm.bridge.emit(data)
}

func (vm *runtimeVM) complete(completion runtimeCompletion) error {
	if !vm.bridge.consume(completion) {
		return errors.New("host completion references an unknown or settled request")
	}
	data, err := json.Marshal(completion)
	if err != nil {
		return errors.Wrap(err, "serialize host completion")
	}
	value, err := vm.newString(data)
	if err != nil {
		return err
	}
	defer vm.freeValue(value)
	result, err := vm.invoke(vm.settle, value)
	if err != nil {
		return err
	}
	vm.freeValue(result)
	return nil
}
