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
  let nextID = 0;

  function serialize(value) {
    return stringify(value, (_key, item) => {
      const type = typeof item;
      if (type === "function" || type === "symbol" || type === "bigint" || type === "undefined") {
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
          reject(hostError(parse(failure), operation));
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
    log: (...args) => emit(args.map(value => typeof value === "string" ? value : serialize(value)).join(" ")),
  });
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
    formatError(error) {
      const data = { kind: "script_error", message: "JavaScript execution failed" };
      try {
        if (error instanceof NativeError) {
          data.message = toString(error.message || error.name);
          if (error.stack) data.message += "\n" + toString(error.stack);
          for (const key of keys(error)) {
            if (key !== "__proto__" && key !== "constructor" && key !== "toJSON") data[key] = error[key];
          }
        } else {
          data.message = toString(error);
        }
        return stringify(data);
      } catch (_) {
        return '{"kind":"script_error","message":"JavaScript threw an unserializable error"}';
      }
    },
  });
})()
