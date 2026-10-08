// The returned closures are retained only by the Go VM owner, never globals.
(() => {
  "use strict";
  const native = globalThis.__kodelet_host;
  delete globalThis.__kodelet_host;
  const stringify = JSON.stringify;
  const parse = JSON.parse;
  const NativePromise = Promise;
  const NativeError = Error;
  const NativeTypeError = TypeError;
  const keys = Object.keys;
  const ownKeys = Reflect.ownKeys;
  const hasOwn = Object.hasOwn;
  const isArray = Array.isArray;
  const create = Object.create;
  const getPrototypeOf = Object.getPrototypeOf;
  const objectPrototype = Object.prototype;
  const define = Object.defineProperty;
  const freeze = Object.freeze;
  const pending = new Map();
  const get = Map.prototype.get.bind(pending);
  const set = Map.prototype.set.bind(pending);
  const remove = Map.prototype.delete.bind(pending);
  const isFiniteNumber = Number.isFinite;
  const toString = String;
  const getOwnPropertyDescriptor = Object.getOwnPropertyDescriptor;
  const apply = Reflect.apply;
  const stringSlice = String.prototype.slice;
  // Host-created errors are rejected from the settle callback, so their stack
  // shows only runtime internals rather than the script's await site.
  const hostErrors = new WeakSet();
  const addHostError = WeakSet.prototype.add;
  const isHostError = WeakSet.prototype.has;
  // Captured before guest code runs, so failure formatting never calls a
  // guest-defined stack accessor.
  const nativeStack = getOwnPropertyDescriptor(NativeError.prototype, "stack");
  const stackGetter = nativeStack !== undefined && typeof nativeStack.get === "function" ? nativeStack.get : undefined;
  // Failure diagnostics are capped in UTF-16 units. Even when every unit is
  // escaped as six JSON bytes, the formatted failure fits the host read limit.
  const messageLimit = 4096;
  const stackLimit = 2048;
  const fieldLimit = 256;
  let nextID = 0;

  function serialize(value) {
    const json = stringify(value, (_key, item) => {
      const type = typeof item;
      // Match JSON.stringify: omit undefined object fields, null in arrays.
      if (type === "undefined") return undefined;
      if (type === "function" || type === "symbol" || type === "bigint") {
        throw new NativeTypeError("Only JSON values can cross the host bridge");
      }
      if (type === "number" && !isFiniteNumber(item)) {
        throw new NativeTypeError("Non-finite numbers are not JSON values");
      }
      if (item instanceof NativePromise) {
        throw new NativeTypeError("Await promises before emitting or passing them to the host");
      }
      if (item instanceof NativeError) {
        const data = { name: item.name, message: item.message };
        for (const key of keys(item)) data[key] = item[key];
        return data;
      }
      return item;
    });
    if (typeof json !== "string") throw new NativeTypeError("Only JSON values can cross the host bridge");
    return json;
  }

  function hostError(fields, operation) {
    const error = new NativeError(fields.message || "Host operation failed");
    error.name = operation === "tool.call" ? "ToolError"
      : operation === "emit" || operation === "emit.image" || operation === "emit.artifact" ? "OutputError"
      : "CatalogError";
    for (const key of keys(fields)) {
      if (key !== "__proto__" && key !== "constructor" && key !== "toJSON") {
        define(error, key, { value: fields[key], enumerable: true, configurable: true });
      }
    }
    define(error, "toJSON", { value: () => fields });
    apply(addHostError, hostErrors, [error]);
    return error;
  }

  function request(operation, args) {
    return new NativePromise((resolve, reject) => {
      const id = ++nextID;
      try {
        const payload = serialize({ id, request: { operation, ...args } });
        set(id, { resolve, reject, operation });
        const failure = native("submit", payload);
        if (failure !== undefined) {
          remove(id);
          const fields = parse(failure);
          // Admission failures happen before dispatch. Oversized payloads are
          // rejected before the host can decode the tool name.
          if (operation === "tool.call" && typeof args.name === "string") {
            if (!hasOwn(fields, "tool")) fields.tool = args.name;
            if (!hasOwn(fields, "outcome")) fields.outcome = "not_started";
          }
          reject(hostError(fields, operation));
        }
      } catch (error) {
        remove(id);
        if (operation === "tool.call" && typeof args.name === "string") {
          error.tool = args.name;
          error.outcome = "not_started";
          error.kind = "invalid_input";
        }
        reject(error);
      }
    });
  }

  const tools = new Proxy(Object.create(null), {
    get(_target, name) {
      if (typeof name !== "string") return undefined;
      return (...args) => {
        // Promise assimilation invokes then(resolve, reject). Reject that
        // synchronously without reserving the registered tool name "then".
        if (args.length > 1) {
          throw hostError({
            kind: "invalid_input", tool: name, outcome: "not_started",
            message: "Call tools[name](input) with one JSON object; the tools namespace is not a promise",
          }, "tool.call");
        }
        return request("tool.call", { name, input: args[0] });
      };
    },
  });
  const catalog = freeze({
    list: (options = {}) => request("catalog.list", { options }),
    search: (query, options = {}) => request("catalog.search", { query, options }),
    describe: name => request("catalog.describe", { name }),
  });
  function emitOutput(operation, value) {
    const failure = native(operation, serialize(value));
    if (failure !== undefined) throw hostError(parse(failure), operation);
  }
  const emit = value => emitOutput("emit", value);

  function isDescriptor(value) {
    return value !== null && typeof value === "object" && !isArray(value)
      && !(value instanceof NativePromise) && !("then" in value)
      && (getPrototypeOf(value) === objectPrototype || getPrototypeOf(value) === null);
  }

  function mediaReference(ref) {
    // Attachment metadata is not forwarded or interpreted. Only its exact ID
    // can be authorized; toJSON cannot manufacture a reference from a promise.
    const artifactId = isDescriptor(ref) && hasOwn(ref, "artifactId") ? ref.artifactId : ref;
    if (typeof artifactId !== "string") {
      throw new NativeTypeError("Media output requires an artifact ID string or descriptor with artifactId");
    }
    const output = create(null);
    output.artifactId = artifactId;
    return output;
  }

  define(emit, "image", { value: (...args) => {
    if (args.length < 1 || args.length > 2) {
      throw new NativeTypeError("emit.image expects a reference and optional detail options");
    }
    const output = mediaReference(args[0]);
    const options = args[1];
    if (options !== undefined) {
      if (!isDescriptor(options) || ownKeys(options).some(key => key !== "detail")
        || ("detail" in options && (!hasOwn(options, "detail") || options.detail !== "original"))) {
        throw new NativeTypeError("Image options accept only detail: original");
      }
      if (hasOwn(options, "detail")) output.detail = options.detail;
    }
    emitOutput("emit.image", output);
  } });
  define(emit, "artifact", { value: (...args) => {
    if (args.length !== 1) throw new NativeTypeError("emit.artifact expects one reference and no options");
    emitOutput("emit.artifact", mediaReference(args[0]));
  } });
  freeze(emit);
  const console = freeze({
    log: (...args) => emit(args.map(value => typeof value === "string" ? value
      : value === undefined ? "undefined" : serialize(value)).join(" ")),
  });
  // Failure formatting reads only data properties, so guest getters, Proxy
  // results, and toString methods cannot change provenance or exceed limits.
  function dataProperty(value, key) {
    let target = value;
    for (let depth = 0; depth < 32 && target !== null
      && (typeof target === "object" || typeof target === "function"); depth++) {
      const descriptor = getOwnPropertyDescriptor(target, key);
      if (descriptor !== undefined) return hasOwn(descriptor, "value") ? descriptor.value : undefined;
      target = getPrototypeOf(target);
    }
    return undefined;
  }

  function errorStack(error) {
    const own = getOwnPropertyDescriptor(error, "stack");
    if (own !== undefined) return hasOwn(own, "value") ? own.value : undefined;
    if (stackGetter === undefined) return undefined;
    try {
      return apply(stackGetter, error, []);
    } catch (_) {
      return undefined;
    }
  }

  function boundedText(value, limit) {
    if (typeof value !== "string") return undefined;
    if (value.length <= limit) return value;
    return apply(stringSlice, value, [0, limit]) + "... [truncated]";
  }

  for (const [name, value] of [["tools", tools], ["catalog", catalog], ["emit", emit], ["console", console]]) {
    define(globalThis, name, { value, enumerable: true });
  }

  return freeze({
    settle(payload) {
      const completion = parse(payload);
      const promise = get(completion.id);
      if (!promise) throw new NativeError("Unknown host completion");
      remove(completion.id);
      if (completion.success) promise.resolve(completion.value);
      else promise.reject(hostError(completion.value, promise.operation));
    },
    serialize,
    // formatError returns a bounded diagnostic: message, provenance, and a
    // trimmed stack. It never includes an error's optional result reply, which
    // remains available to scripts that catch the error.
    formatError(error) {
      try {
        const data = create(null);
        data.kind = "script_error";
        data.message = "JavaScript execution failed";
        if (error === null || (typeof error !== "object" && typeof error !== "function")) {
          // Primitive conversion cannot run guest code.
          data.message = boundedText(toString(error), messageLimit);
          return stringify(data);
        }
        const name = boundedText(dataProperty(error, "name"), fieldLimit);
        const message = boundedText(dataProperty(error, "message"), messageLimit);
        const stack = apply(isHostError, hostErrors, [error]) ? undefined
          : boundedText(errorStack(error), stackLimit);
        const kind = boundedText(dataProperty(error, "kind"), fieldLimit);
        const tool = boundedText(dataProperty(error, "tool"), fieldLimit);
        const callId = boundedText(dataProperty(error, "callId"), fieldLimit);
        const outcome = boundedText(dataProperty(error, "outcome"), fieldLimit);
        let text = message || name || "JavaScript threw a non-Error object";
        if (stack) text += "\n" + stack;
        data.message = text;
        if (kind !== undefined) data.kind = kind;
        if (tool !== undefined) data.tool = tool;
        if (callId !== undefined) data.callId = callId;
        if (outcome !== undefined) data.outcome = outcome;
        return stringify(data);
      } catch (_) {
        return '{"kind":"script_error","message":"JavaScript threw an unserializable error"}';
      }
    },
  });
})()
