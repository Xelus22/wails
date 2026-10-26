/*
 _	   __	  _ __
| |	 / /___ _(_) /____
| | /| / / __ `/ / / ___/
|__/|__/\__,_/_/_/____/
The electron alternative for Go
(c) Lea Anthony 2019-present
*/

/**
 * The canonical JSON shape of a runtime call *error* response,
 * shared by the HTTP transport and the wasm bridge transport.
 */
export type CallErrorType = {
    message: string,
    cause?: unknown,
    kind: "ReferenceError" | "TypeError" | "RuntimeError"
}

/**
 * Exception class that will be thrown in case the bound method returns an error.
 * The value of the {@link RuntimeError#name} property is "RuntimeError".
 */
export class RuntimeError extends Error {
    /**
     * Constructs a new RuntimeError instance.
     * @param message - The error message.
     * @param options - Options to be forwarded to the Error constructor.
     */
    constructor(message?: string, options?: ErrorOptions) {
        super(message, options);
        this.name = "RuntimeError";
    }
}

/**
 * Builds the exception to reject a failed runtime call with,
 * from its canonical error JSON.
 * Shared by the HTTP transport and the wasm bridge transport so both
 * produce identical error types.
 *
 * @param json - The parsed error response.
 * @returns The error to throw.
 */
export function errorFromCallJSON(json: CallErrorType): Error {
    let err: Error;
    switch (json.kind) {
        case "ReferenceError": err = new ReferenceError(json.message); break;
        case "TypeError":      err = new TypeError(json.message); break;
        case "RuntimeError":   err = new RuntimeError(json.message); break;
        default:               err = new Error(json.message);
    }
    err.cause = json.cause;
    return err;
}
