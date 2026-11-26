"""Select the pinned 1.4.29 upgrade recipe with the unchanged verified builder."""

import importlib.util
from pathlib import Path

if __name__ == "__main__":
    source = Path(__file__).with_name("build_vendor_wheel.py")
    spec = importlib.util.spec_from_file_location("liberty_vendor_builder", source)
    build_vendor_wheel = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(build_vendor_wheel)
    build_vendor_wheel.RECIPE_PATH = Path(__file__).resolve().parents[1] / "vendor" / "funasr-1.4.29.json"
    raise SystemExit(build_vendor_wheel.main())
