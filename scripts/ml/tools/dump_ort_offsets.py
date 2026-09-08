"""Print byte offsets of key OrtApi members for your installed ORT version."""
import onnxruntime as ort
import ctypes, os, sys

# Load the DLL
if sys.platform == "win32":
    dll_path = os.path.join(os.path.dirname(ort.__file__), "capi", "onnxruntime.dll")
    lib = ctypes.CDLL(dll_path)
else:
    lib = ctypes.CDLL("libonnxruntime.so")

OrtGetApiBase = lib.OrtGetApiBase
OrtGetApiBase.restype = ctypes.c_void_p

api_base = OrtGetApiBase()
# GetApi is at offset 0 in OrtApiBase (first function pointer)
GetApi = ctypes.cast(ctypes.c_void_p.from_address(api_base), ctypes.CFUNCTYPE(ctypes.c_void_p, ctypes.c_uint32))
api = GetApi(16)  # request API version 16

print(f"ORT version : {ort.__version__}")
print(f"DLL         : {dll_path if sys.platform == 'win32' else 'libonnxruntime.so'}")
print(f"OrtApi*     = {api:#x}")
print()
print(f"{'idx':>4}  {'offset':>6}  {'fp':>18}")
print("-" * 35)
for i in range(90):
    fp = ctypes.c_void_p.from_address(api + i * 8).value
    print(f"  [{i:2d}]  {i*8:5d}   {fp:#018x}")
