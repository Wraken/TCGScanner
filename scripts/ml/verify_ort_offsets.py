"""
Verify OrtApi function pointer offsets by calling simple utility functions first,
then working up to the ones we need.
"""
import ctypes, os, sys
import onnxruntime as ort

if sys.platform == "win32":
    dll_path = os.path.join(os.path.dirname(ort.__file__), "capi", "onnxruntime.dll")
    lib = ctypes.CDLL(dll_path)
else:
    lib = ctypes.CDLL("libonnxruntime.so")

lib.OrtGetApiBase.restype = ctypes.c_void_p
api_base = lib.OrtGetApiBase()

# Print OrtApiBase version string (index 1 in OrtApiBase)
get_ver_str = ctypes.cast(
    ctypes.c_void_p.from_address(api_base + 8).value or 0,
    ctypes.CFUNCTYPE(ctypes.c_char_p),
)
print(f"ORT DLL version string: {get_ver_str()}")

# Get OrtApi* — try current version (21) then fall back
GetApi = ctypes.cast(
    ctypes.c_void_p.from_address(api_base).value or 0,
    ctypes.CFUNCTYPE(ctypes.c_void_p, ctypes.c_uint32),
)
api21 = GetApi(21)
api16 = GetApi(16)
print(f"GetApi(16) = {api16:#x}")
print(f"GetApi(21) = {api21:#x}")
api = api21 or api16
print(f"Using api  = {api:#x}\n")

def fn(index, restype, *argtypes):
    ptr = ctypes.c_void_p.from_address(api + index * 8).value or 0
    return ctypes.cast(ptr, ctypes.CFUNCTYPE(restype, *argtypes))

# ── Step 1: CreateStatus [0] + GetErrorCode [1] + GetErrorMessage [2] + ReleaseStatus [3] ──
print("=== Index 0-3: Status utilities ===")

# [0] CreateStatus(OrtErrorCode code, const char* msg) -> OrtStatus*
status_ptr = fn(0, ctypes.c_void_p, ctypes.c_int, ctypes.c_char_p)(1, b"test error")
print(f"  [0] CreateStatus        -> {status_ptr:#x}  ({'non-null ✓' if status_ptr else 'NULL ✗'})")

# [1] GetErrorCode(OrtStatus*) -> OrtErrorCode (int)
if status_ptr:
    code = fn(1, ctypes.c_int, ctypes.c_void_p)(status_ptr)
    print(f"  [1] GetErrorCode        -> {code}  ({'= 1 ✓' if code == 1 else f'expected 1, got {code} ✗'})")

# [2] GetErrorMessage(OrtStatus*) -> const char*
if status_ptr:
    msg = fn(2, ctypes.c_char_p, ctypes.c_void_p)(status_ptr)
    print(f"  [2] GetErrorMessage     -> {msg}  ({'✓' if msg == b'test error' else '✗'})")

# [3] ReleaseStatus(OrtStatus*)
if status_ptr:
    fn(3, None, ctypes.c_void_p)(status_ptr)
    print(f"  [3] ReleaseStatus       -> no crash ✓")

# ── Step 2: CreateEnv [4] ──────────────────────────────────────────────────────
print("\n=== Index 4: CreateEnv ===")
env_ptr = ctypes.c_void_p(0)
try:
    status = fn(4, ctypes.c_void_p,
                ctypes.c_int, ctypes.c_char_p,
                ctypes.POINTER(ctypes.c_void_p))(
        3, b"verify", ctypes.byref(env_ptr)
    )
    print(f"  [4] CreateEnv           -> env={env_ptr.value:#x}  status={status:#x if status else 0}")
except OSError as e:
    print(f"  [4] CRASH: {e}")
    print("       → index 4 may not be CreateEnv in this ORT version")

# ── Step 3: CreateSessionOptions [11] ─────────────────────────────────────────
print("\n=== Index 11: CreateSessionOptions ===")
opts_ptr = ctypes.c_void_p(0)
try:
    fn(11, ctypes.c_void_p, ctypes.POINTER(ctypes.c_void_p))(ctypes.byref(opts_ptr))
    print(f"  [11] CreateSessionOptions -> opts={opts_ptr.value:#x}  {'✓' if opts_ptr.value else '✗'}")
except OSError as e:
    print(f"  [11] CRASH: {e}")

# ── Step 4: Tentative Release offsets ─────────────────────────────────────────
if opts_ptr.value:
    print("\n=== Tentative Release offsets ===")
    for idx in [84, 85, 86, 87]:
        try:
            fn(idx, None, ctypes.c_void_p)(ctypes.c_void_p(opts_ptr.value))
            print(f"  [{idx}] no crash ✓  (could be ReleaseSessionOptions)")
            break
        except OSError as e:
            print(f"  [{idx}] crash: {e}")
