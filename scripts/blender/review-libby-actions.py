"""Create all 135 image-space comparisons and a review index.

Silhouette overlap and RGB error are diagnostic measurements, not a likeness
score and never an automatic visual-acceptance gate. Requires Pillow + numpy.
"""
import argparse
import json
from pathlib import Path

import numpy as np
from PIL import Image,ImageDraw,ImageFont

parser=argparse.ArgumentParser();parser.add_argument('repo',type=Path);parser.add_argument('output',type=Path);args=parser.parse_args()
repo=args.repo.resolve();output=args.output.resolve();output.mkdir(parents=True,exist_ok=True)
assets=repo/'packages/desktop-resources/pet/models/candidates/pixel-actions'
manifest=json.loads((assets/'manifest.json').read_text())
captures=repo/'target/native/pet3d-fidelity/actions'
font=ImageFont.load_default(size=18);small=ImageFont.load_default(size=13)
summary=[]
overview=Image.new('RGB',(5*250,3*285),'#eeeee9')
for index,action in enumerate(manifest['actions']):
    name=action['id'];board=Image.new('RGB',(9*250,3*270),'#eeeee9');draw=ImageDraw.Draw(board)
    frames=[]
    for frame in range(1,10):
        source=Image.open(repo/'packages/desktop-resources'/action['references'][frame-1]['file']).convert('RGBA')
        model=Image.open(captures/name/f'frame-{frame:02}.png').convert('RGBA')
        a,b=np.asarray(source).astype(float),np.asarray(model).astype(float)
        am,bm=a[:,:,3]>128,b[:,:,3]>128
        silhouette=float((am&bm).sum()/max(1,(am|bm).sum()))
        color=float(np.abs(a[:,:,:3]-b[:,:,:3])[am&bm].mean())
        frames.append({'frame':frame,'silhouetteIntersectionOverUnion':round(silhouette,6),'meanRGBAbsoluteErrorOnOverlap':round(color,4)})
        for row,image in enumerate([source,model]):
            thumbnail=image.resize((246,246),Image.Resampling.NEAREST)
            board.paste(thumbnail,((frame-1)*250,row*270+20),thumbnail)
        diff=np.zeros((418,418,4),dtype=np.uint8)
        diff[am&~bm]=[220,62,62,255];diff[~am&bm]=[46,119,209,255]
        diff[am&bm]=[205,210,198,160]
        image=Image.fromarray(diff).resize((246,246),Image.Resampling.NEAREST)
        board.paste(image,((frame-1)*250,560),image)
        draw.text(((frame-1)*250+6,2),f'{name} · frame {frame}',fill='#27352b',font=small)
    board.save(output/(name+'-nine-frames.png'))
    front=Image.open(captures/name/'frame-01.png').convert('RGBA').resize((246,246),Image.Resampling.LANCZOS)
    px,py=(index%5)*250,(index//5)*285
    overview.paste(front,(px,py+25),front);ImageDraw.Draw(overview).text((px+8,py+5),name,fill='#27352b',font=font)
    summary.append({'id':name,'frames':frames,'status':'visual-review-required'})
overview.save(output/'all-actions.png')
(output/'comparison-metrics.json').write_text(json.dumps({'warning':'Diagnostic geometry/colour measurements only. These numbers do not establish 1:1 likeness or completion.','actions':summary},indent=2)+'\n')
print('Saved 15 nine-frame comparisons, 135 diagnostic measurements and overview.')
