"""Offline reference motion measurement (OpenCV 4.11.0.86 + numpy).

This produces editable fitting inputs only. No OpenCV dependency is added to
the desktop application. Each field maps the first drawing into a later pose.
"""
import argparse
import hashlib
import json
from pathlib import Path
import sys
import platform
import urllib.request
import zipfile

parser=argparse.ArgumentParser();parser.add_argument('repo',type=Path);parser.add_argument('output',type=Path)
args=parser.parse_args(sys.argv[sys.argv.index('--')+1:] if '--' in sys.argv else None)
repo=args.repo.resolve();output=args.output.resolve();output.mkdir(parents=True,exist_ok=True)
vendor=repo/'target/native/pet3d-fidelity/tools/opencv'
if not (vendor/'package/cv2').is_dir():
    if platform.system()!='Darwin' or platform.machine()!='arm64':
        raise RuntimeError('Offline fitting tool currently verified on macOS arm64 only; provide OpenCV 4.11.0.86 for this platform.')
    url='https://files.pythonhosted.org/packages/dc/53/2c50afa0b1e05ecdb4603818e85f7d174e683d874ef63a6abe3ac92220c8/opencv_python_headless-4.11.0.86-cp37-abi3-macosx_13_0_arm64.whl'
    expected='48128188ade4a7e517237c8e1e11a9cdf5c282761473383e77beb875bb1e61ca'
    vendor.mkdir(parents=True,exist_ok=True);wheel=vendor/'opencv-4.11.0.86.whl'
    payload=urllib.request.urlopen(url).read()
    if hashlib.sha256(payload).hexdigest()!=expected:raise RuntimeError('OpenCV integrity mismatch')
    wheel.write_bytes(payload)
    with zipfile.ZipFile(wheel) as archive:
        for entry in archive.infolist():
            destination=(vendor/'package'/entry.filename).resolve()
            if not destination.is_relative_to((vendor/'package').resolve()):raise RuntimeError('Invalid archive path')
        archive.extractall(vendor/'package')
    (vendor/'provenance.json').write_text(json.dumps({'version':'4.11.0.86','url':url,'sha256':expected},indent=2)+'\n')
sys.path.insert(0,str(vendor/'package'))
import cv2
import numpy as np
assert cv2.__version__=='4.11.0'
for folder in sorted((repo/'packages/desktop-resources/images/pet/action').iterdir()):
    if not folder.is_dir():continue
    images=[];refs=[]
    for file in sorted(folder.glob('*.png')):
        im=cv2.imread(str(file),cv2.IMREAD_UNCHANGED)
        alpha=im[:,:,3:4].astype(float)/255
        rgb=im[:,:,:3]*alpha+np.full_like(im[:,:,:3],236)*(1-alpha)
        gray=cv2.cvtColor(rgb.astype(np.uint8),cv2.COLOR_BGR2GRAY)
        images.append(gray);refs.append(hashlib.sha256(file.read_bytes()).hexdigest())
    flows=[np.zeros((209,209,2),np.float32)];backs=[np.zeros((209,209,2),np.float32)];confidence=[np.ones((209,209),np.float32)]
    y,x=np.indices((418,418),dtype=np.float32)
    for image in images[1:]:
        engine=cv2.DISOpticalFlow_create(cv2.DISOPTICAL_FLOW_PRESET_MEDIUM);engine.setFinestScale(0)
        forward=engine.calc(images[0],image,None)
        backward=engine.calc(image,images[0],None)
        sampled=cv2.remap(backward,x+forward[:,:,0],y+forward[:,:,1],cv2.INTER_LINEAR,borderMode=cv2.BORDER_REPLICATE)
        error=np.linalg.norm(forward+sampled,axis=2)
        flows.append(cv2.resize(forward,(209,209),interpolation=cv2.INTER_AREA))
        backs.append(cv2.resize(backward,(209,209),interpolation=cv2.INTER_AREA))
        confidence.append(cv2.resize((error<3).astype(np.float32),(209,209),interpolation=cv2.INTER_AREA))
    np.savez_compressed(output/(folder.name+'.npz'),flow=np.asarray(flows),backward=np.asarray(backs),confidence=np.asarray(confidence))
    (output/(folder.name+'.json')).write_text(json.dumps({'action':folder.name,'opencv':'4.11.0.86','algorithm':'DIS medium, finest scale 0; forward/back consistency','references':refs},indent=2)+'\n')
    print(folder.name,'motion measured',flush=True)
