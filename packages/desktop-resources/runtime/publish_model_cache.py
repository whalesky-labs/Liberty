
import json
import os
import pickle
import sys
import tempfile

marker_path, models_root = sys.argv[1:3]
with open(marker_path, "r", encoding="utf-8") as stream:
    marker = json.load(stream)

def atomic_pickle(path, value):
    fd, temp_path = tempfile.mkstemp(dir=os.path.dirname(path))
    try:
        with os.fdopen(fd, "wb") as stream:
            pickle.dump(value, stream)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp_path, path)
    except BaseException:
        try:
            os.unlink(temp_path)
        except OSError:
            pass
        raise

for model in marker["models"]:
    model_root = os.path.join(models_root, *model["relativePath"].split("/"))
    atomic_pickle(
        os.path.join(model_root, ".msc"),
        [{"Path": item["path"], "Revision": item["revision"]} for item in model["files"]],
    )
    atomic_pickle(os.path.join(model_root, ".mdl"), {"id": model["modelId"]})
