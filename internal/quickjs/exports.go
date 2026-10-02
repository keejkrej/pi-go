// Ported from node_modules/quickjs-wasi/dist/index.js (quickjs-wasi 3.6.2).

package quickjs

// exportID names a quickjs.wasm export called by the glue (TS: vm.exports.<name>).
type exportID int

const (
	exInitialize exportID = iota
	exWasmMalloc
	exWasmFree
	exQjsCall
	exQjsCallConstructor
	exQjsCompile
	exQjsComputeMemoryUsage
	exQjsDefinePropString
	exQjsDefinePropValue
	exQjsDupValue
	exQjsEval
	exQjsEvalBytecode
	exQjsExecutePendingJob
	exQjsFreeCstring
	exQjsFreeValue
	exQjsGetArrayBuffer
	exQjsGetBigInt64
	exQjsGetBool
	exQjsGetClassId
	exQjsGetClassName
	exQjsGetContextPtr
	exQjsGetException
	exQjsGetFalse
	exQjsGetFloat64
	exQjsGetGcThreshold
	exQjsGetGlobal
	exQjsGetNull
	exQjsGetOwnPropertyDescriptor
	exQjsGetOwnPropertyKeys
	exQjsGetOwnPropertyNames
	exQjsGetOwnPropertyNamesAll
	exQjsGetPropString
	exQjsGetPropUint32
	exQjsGetPropValue
	exQjsGetPrototypeOf
	exQjsGetProxyHandler
	exQjsGetProxyTarget
	exQjsGetQuickjsVersion
	exQjsGetRuntimePtr
	exQjsGetStringLen
	exQjsGetSymbolDescription
	exQjsGetTrue
	exQjsGetTypedArrayBuffer
	exQjsGetUndefined
	exQjsGetValuePtr
	exQjsHasOwnProperty
	exQjsHasOwnPropertyValue
	exQjsInit
	exQjsInit2
	exQjsIsArray
	exQjsIsArrayBuffer
	exQjsIsBigInt
	exQjsIsBool
	exQjsIsDataView
	exQjsIsDate
	exQjsIsError
	exQjsIsException
	exQjsIsFunction
	exQjsIsJobPending
	exQjsIsMap
	exQjsIsNull
	exQjsIsNumber
	exQjsIsObject
	exQjsIsPromise
	exQjsIsProxy
	exQjsIsRegexp
	exQjsIsSet
	exQjsIsString
	exQjsIsSymbol
	exQjsIsUndefined
	exQjsIsWeakMap
	exQjsIsWeakRef
	exQjsIsWeakSet
	exQjsNewArray
	exQjsNewArrayBuffer
	exQjsNewBigInt64
	exQjsNewError
	exQjsNewHostFunction
	exQjsNewNumber
	exQjsNewObject
	exQjsNewPromise
	exQjsNewString
	exQjsNewSymbol
	exQjsNewUint8Array
	exQjsPromiseMarkAsHandled
	exQjsPromiseResult
	exQjsPromiseState
	exQjsPromiseThen
	exQjsPropertyIsEnumerable
	exQjsPropertyIsEnumerableValue
	exQjsRunGc
	exQjsSetGcThreshold
	exQjsSetInterruptHandler
	exQjsSetMaxStackSize
	exQjsSetMemoryLimit
	exQjsSetModuleLoader
	exQjsSetPromiseRejectionHandler
	exQjsSetPropString
	exQjsSetPropUint32
	exQjsSetPropValue
	exQjsSetRuntimeAndContext
	exQjsThrow
	exportCount
)

var exportNames = [exportCount]string{
	exInitialize:                    "_initialize",
	exWasmMalloc:                    "wasm_malloc",
	exWasmFree:                      "wasm_free",
	exQjsCall:                       "qjs_call",
	exQjsCallConstructor:            "qjs_call_constructor",
	exQjsCompile:                    "qjs_compile",
	exQjsComputeMemoryUsage:         "qjs_compute_memory_usage",
	exQjsDefinePropString:           "qjs_define_prop_string",
	exQjsDefinePropValue:            "qjs_define_prop_value",
	exQjsDupValue:                   "qjs_dup_value",
	exQjsEval:                       "qjs_eval",
	exQjsEvalBytecode:               "qjs_eval_bytecode",
	exQjsExecutePendingJob:          "qjs_execute_pending_job",
	exQjsFreeCstring:                "qjs_free_cstring",
	exQjsFreeValue:                  "qjs_free_value",
	exQjsGetArrayBuffer:             "qjs_get_array_buffer",
	exQjsGetBigInt64:                "qjs_get_big_int64",
	exQjsGetBool:                    "qjs_get_bool",
	exQjsGetClassId:                 "qjs_get_class_id",
	exQjsGetClassName:               "qjs_get_class_name",
	exQjsGetContextPtr:              "qjs_get_context_ptr",
	exQjsGetException:               "qjs_get_exception",
	exQjsGetFalse:                   "qjs_get_false",
	exQjsGetFloat64:                 "qjs_get_float64",
	exQjsGetGcThreshold:             "qjs_get_gc_threshold",
	exQjsGetGlobal:                  "qjs_get_global",
	exQjsGetNull:                    "qjs_get_null",
	exQjsGetOwnPropertyDescriptor:   "qjs_get_own_property_descriptor",
	exQjsGetOwnPropertyKeys:         "qjs_get_own_property_keys",
	exQjsGetOwnPropertyNames:        "qjs_get_own_property_names",
	exQjsGetOwnPropertyNamesAll:     "qjs_get_own_property_names_all",
	exQjsGetPropString:              "qjs_get_prop_string",
	exQjsGetPropUint32:              "qjs_get_prop_uint32",
	exQjsGetPropValue:               "qjs_get_prop_value",
	exQjsGetPrototypeOf:             "qjs_get_prototype_of",
	exQjsGetProxyHandler:            "qjs_get_proxy_handler",
	exQjsGetProxyTarget:             "qjs_get_proxy_target",
	exQjsGetQuickjsVersion:          "qjs_get_quickjs_version",
	exQjsGetRuntimePtr:              "qjs_get_runtime_ptr",
	exQjsGetStringLen:               "qjs_get_string_len",
	exQjsGetSymbolDescription:       "qjs_get_symbol_description",
	exQjsGetTrue:                    "qjs_get_true",
	exQjsGetTypedArrayBuffer:        "qjs_get_typed_array_buffer",
	exQjsGetUndefined:               "qjs_get_undefined",
	exQjsGetValuePtr:                "qjs_get_value_ptr",
	exQjsHasOwnProperty:             "qjs_has_own_property",
	exQjsHasOwnPropertyValue:        "qjs_has_own_property_value",
	exQjsInit:                       "qjs_init",
	exQjsInit2:                      "qjs_init2",
	exQjsIsArray:                    "qjs_is_array",
	exQjsIsArrayBuffer:              "qjs_is_array_buffer",
	exQjsIsBigInt:                   "qjs_is_big_int",
	exQjsIsBool:                     "qjs_is_bool",
	exQjsIsDataView:                 "qjs_is_data_view",
	exQjsIsDate:                     "qjs_is_date",
	exQjsIsError:                    "qjs_is_error",
	exQjsIsException:                "qjs_is_exception",
	exQjsIsFunction:                 "qjs_is_function",
	exQjsIsJobPending:               "qjs_is_job_pending",
	exQjsIsMap:                      "qjs_is_map",
	exQjsIsNull:                     "qjs_is_null",
	exQjsIsNumber:                   "qjs_is_number",
	exQjsIsObject:                   "qjs_is_object",
	exQjsIsPromise:                  "qjs_is_promise",
	exQjsIsProxy:                    "qjs_is_proxy",
	exQjsIsRegexp:                   "qjs_is_regexp",
	exQjsIsSet:                      "qjs_is_set",
	exQjsIsString:                   "qjs_is_string",
	exQjsIsSymbol:                   "qjs_is_symbol",
	exQjsIsUndefined:                "qjs_is_undefined",
	exQjsIsWeakMap:                  "qjs_is_weak_map",
	exQjsIsWeakRef:                  "qjs_is_weak_ref",
	exQjsIsWeakSet:                  "qjs_is_weak_set",
	exQjsNewArray:                   "qjs_new_array",
	exQjsNewArrayBuffer:             "qjs_new_array_buffer",
	exQjsNewBigInt64:                "qjs_new_big_int64",
	exQjsNewError:                   "qjs_new_error",
	exQjsNewHostFunction:            "qjs_new_host_function",
	exQjsNewNumber:                  "qjs_new_number",
	exQjsNewObject:                  "qjs_new_object",
	exQjsNewPromise:                 "qjs_new_promise",
	exQjsNewString:                  "qjs_new_string",
	exQjsNewSymbol:                  "qjs_new_symbol",
	exQjsNewUint8Array:              "qjs_new_uint8_array",
	exQjsPromiseMarkAsHandled:       "qjs_promise_mark_as_handled",
	exQjsPromiseResult:              "qjs_promise_result",
	exQjsPromiseState:               "qjs_promise_state",
	exQjsPromiseThen:                "qjs_promise_then",
	exQjsPropertyIsEnumerable:       "qjs_property_is_enumerable",
	exQjsPropertyIsEnumerableValue:  "qjs_property_is_enumerable_value",
	exQjsRunGc:                      "qjs_run_gc",
	exQjsSetGcThreshold:             "qjs_set_gc_threshold",
	exQjsSetInterruptHandler:        "qjs_set_interrupt_handler",
	exQjsSetMaxStackSize:            "qjs_set_max_stack_size",
	exQjsSetMemoryLimit:             "qjs_set_memory_limit",
	exQjsSetModuleLoader:            "qjs_set_module_loader",
	exQjsSetPromiseRejectionHandler: "qjs_set_promise_rejection_handler",
	exQjsSetPropString:              "qjs_set_prop_string",
	exQjsSetPropUint32:              "qjs_set_prop_uint32",
	exQjsSetPropValue:               "qjs_set_prop_value",
	exQjsSetRuntimeAndContext:       "qjs_set_runtime_and_context",
	exQjsThrow:                      "qjs_throw",
}
