"""Build all original Libby actions as articulated, coloured voxel volumes.

Nine reference images constrain each action. The base component meshes remain
present throughout the animation. Bones move bodies and props; eyes/mouth use
geometric voxel morphs. There are no animated image cards or whole-frame meshes.
"""
import argparse
from collections import deque
import hashlib
import json
import math
from pathlib import Path
import sys

import bpy
import bmesh
import numpy as np
from mathutils import Vector, Matrix

sys.path.insert(0,str(Path(__file__).resolve().parent))
from libby_action_specs import SPECS,POSE_OUTLINES,ROPE_PATHS
import libby_voxel_mesh as vox
import libby_pose_volumes as poses
from libby_glb_pack import pack as pack_glb

parser=argparse.ArgumentParser()
parser.add_argument('repo',type=Path)
parser.add_argument('output',type=Path)
parser.add_argument('--action',choices=list(SPECS))
parser.add_argument('--no-render',action='store_true')
args=parser.parse_args(sys.argv[sys.argv.index('--')+1:])
repo,output=args.repo.resolve(),args.output.resolve()
SHAPE=(209,209)
yy,xx=np.indices(SHAPE)
X,Y=xx*2+1,yy*2+1
PALETTE={
 'wool':[246,246,241],'skin':[255,205,161],'horn':[151,87,46],
 'foot':[149,84,45],'wood':[159,84,39],'dark':[46,47,46],
 'gold':[250,191,28],'metal':[174,178,175],'book':[58,53,43],
 'paper':[240,235,220],'pink':[216,82,112],'red':[210,63,47],
 'blue':[29,151,184],'orange':[242,116,28],'car':[233,235,232],
 'rubber':[41,40,36],
}


def polygon(points):
    inside=np.zeros(SHAPE,dtype=bool)
    x0,y0=points[-1]
    for x1,y1 in points:
        if y0!=y1:inside^=((y0>Y)!=(y1>Y))&(X<(x1-x0)*(Y-y0)/(y1-y0)+x0)
        x0,y0=x1,y1
    return inside


def dilation(mask):
    p=np.pad(mask,1)
    return p[1:-1,1:-1]|p[:-2,1:-1]|p[2:,1:-1]|p[1:-1,:-2]|p[1:-1,2:]


def filled(mask):
    outside=np.zeros(SHAPE,dtype=bool);queue=deque([(0,0)])
    while queue:
        y,x=queue.popleft()
        if not(0<=y<209 and 0<=x<209) or outside[y,x] or mask[y,x]:continue
        outside[y,x]=True
        queue.extend([(y-1,x),(y+1,x),(y,x-1),(y,x+1)])
    return ~outside


def distance(mask,limit=70):
    result=np.zeros(SHAPE,dtype=float);layer=mask.copy()
    for _ in range(limit):
        if not layer.any():break
        result[layer]+=2
        p=np.pad(layer,1);layer=p[1:-1,1:-1]&p[:-2,1:-1]&p[2:,1:-1]&p[1:-1,:-2]&p[1:-1,2:]
    return result


def rope_character(mask):
    """Separate thick anatomy from the thin cable and ground shadow."""
    core=mask.copy()
    for _ in range(3):
        padded=np.pad(core,1)
        core=padded[1:-1,1:-1]&padded[:-2,1:-1]&padded[2:,1:-1]&padded[1:-1,:-2]&padded[1:-1,2:]
    remaining=core.copy();largest=[]
    while remaining.any():
        y,x=np.argwhere(remaining)[0];queue=deque([(int(y),int(x))]);component=[]
        while queue:
            y,x=queue.popleft()
            if not (0<=y<209 and 0<=x<209) or not remaining[y,x]:continue
            remaining[y,x]=False;component.append((y,x))
            queue.extend([(y-1,x),(y+1,x),(y,x-1),(y,x+1)])
        if len(component)>len(largest):largest=component
    result=np.zeros(SHAPE,dtype=bool)
    for y,x in largest:result[y,x]=True
    for _ in range(5):result=dilation(result)
    return result&mask


def read_images(action):
    raw=[];references=[]
    for frame in range(1,10):
        file=repo/f'packages/desktop-resources/images/pet/action/{action}/{action}_{frame:02}.png'
        im=bpy.data.images.load(str(file),check_existing=False);im.colorspace_settings.name='Non-Color'
        p=np.empty(418*418*4,dtype=np.float32);im.pixels.foreach_get(p)
        p=p.reshape(418,418,4)[::-1].reshape(209,2,209,2,4).mean(axis=(1,3))
        raw.append(p);bpy.data.images.remove(im)
        references.append({'frame':frame,'file':str(file.relative_to(repo/'packages/desktop-resources')),'sha256':hashlib.sha256(file.read_bytes()).hexdigest()})
    raw=np.asarray(raw);rgb=np.clip(np.round(raw[:,:,:,:3]*255/4)*4,0,255).astype(np.uint8)
    valid=raw[:,:,:,3]>.52
    values,counts=np.unique(rgb[valid],axis=0,return_counts=True);values=values.astype(float)
    centers=[values[np.argmax(counts)]];nearest=np.full(len(values),np.inf)
    for _ in range(47):
        nearest=np.minimum(nearest,((values-centers[-1])**2).sum(axis=1));centers.append(values[np.argmax(nearest*np.sqrt(counts))])
    centers=np.array(centers)
    for _ in range(8):
        assignment=((values[:,None]-centers[None])**2).sum(axis=2).argmin(axis=1)
        for k in range(len(centers)):
            mask=assignment==k
            if mask.any():centers[k]=np.average(values[mask],axis=0,weights=counts[mask])
    palette=np.clip(np.round(centers),0,255).astype(np.uint8)
    quantized=[]
    for p in rgb:
        flat=p.reshape(-1,3).astype(float)
        quantized.append(palette[((flat[:,None]-centers[None])**2).sum(axis=2).argmin(axis=1)].reshape(209,209,3))
    return raw,np.asarray(quantized),palette,references


def tracked(raw,mask,max_shift=28):
    """Track a drawn rigid part by reference pixels; return image-space offsets."""
    # Subsample within the component, retaining the outline and coloured ink.
    grid=(xx%3==0)&(yy%3==0)&mask
    sy,sx=np.where(grid)
    if len(sy)<8:return [(0,0)]*9
    template=raw[0,sy,sx,:]
    result=[(0,0)]
    for index in range(1,9):
        best=(float('inf'),0,0)
        for dy in range(-max_shift//2,max_shift//2+1,2):
            for dx in range(-max_shift//2,max_shift//2+1,2):
                ty,tx=sy+dy,sx+dx
                if ty.min()<0 or tx.min()<0 or ty.max()>208 or tx.max()>208:continue
                error=np.abs(raw[index,ty,tx]-template)
                score=float(np.minimum(error[:,:3],.5).mean()+error[:,3].mean()*.5)+.00003*(dx*dx+dy*dy)
                if score<best[0]:best=(score,dx,dy)
        _,bx,by=best
        for dy in range(by-2,by+3):
            for dx in range(bx-2,bx+3):
                ty,tx=sy+dy,sx+dx
                if ty.min()<0 or tx.min()<0 or ty.max()>208 or tx.max()>208:continue
                error=np.abs(raw[index,ty,tx]-template)
                score=float(np.minimum(error[:,:3],.5).mean()+error[:,3].mean()*.5)+.00003*(dx*dx+dy*dy)
                if score<best[0]:best=(score,dx,dy)
        result.append((best[1]*2,best[2]*2))
    return result


def shifted(array,dx,dy):
    # Sample a translated reference back into the first-frame coordinate system.
    return array[np.clip(yy+round(dy/2),0,208),np.clip(xx+round(dx/2),0,208)]


def fit_transforms(flow,confidence,mask,rotate=True):
    """Robust similarity fit from measured visible points to each drawing."""
    transforms=[np.eye(3)]
    sample=mask&(xx%3==0)&(yy%3==0)
    for i in range(1,9):
        selected=sample&(confidence[i]>.6)
        sy,sx=np.where(selected)
        if len(sy)<12:transforms.append(np.eye(3));continue
        source=np.stack([X[sy,sx],Y[sy,sx]],axis=1)
        target=source+flow[i,sy,sx]
        keep=np.ones(len(source),dtype=bool)
        for _ in range(3):
            a,b=source[keep],target[keep]
            ac,bc=a.mean(axis=0),b.mean(axis=0)
            aa,bb=a-ac,b-bc
            u,_,vh=np.linalg.svd(aa.T@bb)
            rotation=vh.T@u.T
            if np.linalg.det(rotation)<0:rotation=np.eye(2)
            theta=math.atan2(rotation[1,0],rotation[0,0]) if rotate else 0
            theta=max(-.6,min(.6,theta));rotation=np.array([[math.cos(theta),-math.sin(theta)],[math.sin(theta),math.cos(theta)]])
            scale=np.sum((aa@rotation.T)*bb)/max(1e-5,np.sum(aa*aa))
            scale=max(.82,min(1.2,scale))
            linear=rotation*scale;offset=bc-linear@ac
            error=np.linalg.norm(source@linear.T+offset-target,axis=1)
            keep=error<max(2.5,float(np.quantile(error,.72)))
        matrix=np.eye(3);matrix[:2,:2]=linear;matrix[:2,2]=offset;transforms.append(matrix)
    return transforms


def sample_transform(array,matrix):
    sx=matrix[0,0]*X+matrix[0,1]*Y+matrix[0,2]
    sy=matrix[1,0]*X+matrix[1,1]*Y+matrix[1,2]
    return array[np.clip(np.rint((sy-1)/2).astype(int),0,208),np.clip(np.rint((sx-1)/2).astype(int),0,208)]


def world_transform(matrix):
    a,b,tx=matrix[0];c,d,ty=matrix[1]
    return Matrix(((a,0,-b,(a*209+b*369+tx-209)*.01),(0,math.sqrt(abs(a*d-b*c)),0,0),(-c,0,d,(369-c*209-d*369-ty)*.01),(0,0,0,1)))


def pose_labels(action, spec, base, ids, raw, colors, measured):
    """Carry semantic ownership into each drawing, then resolve ink edges.

    Optical flow proposes ownership only. Colour families and authored contact
    outlines prevent a hand from taking the sleeve, pencil or background with it.
    The final masks partition the visible source silhouette at every key pose.
    """
    frames=[base.copy()]
    kinds={'body':'wool','hood':'wool','face':'skin',**{p['name']:p['kind'] for p in spec['parts']}}
    for index in range(1,9):
        backward=measured['backward'][index]
        sx=np.clip(np.rint((X+backward[:,:,0]-1)/2).astype(int),0,208)
        sy=np.clip(np.rint((Y+backward[:,:,1]-1)/2).astype(int),0,208)
        labels=base[sy,sx].copy()
        opaque=raw[index,:,:,3]>.52
        if spec.get('exclude'):opaque&=~polygon(spec['exclude'])
        if spec.get('rope'):opaque=rope_character(opaque)
        labels[~opaque]=0
        # Seed colour families before propagating through thin contour ink.
        rgb=colors[index].astype(float);r,g,b=rgb[:,:,0],rgb[:,:,1],rgb[:,:,2]
        skin=(r>155)&(g>85)&(r>b*1.15)&(r>g*1.035)&(r-g<130)
        wool=(r>145)&(g>145)&(b>135)&(np.maximum.reduce([r,g,b])-np.minimum.reduce([r,g,b])<38)
        for name,kind in kinds.items():
            owned=labels==ids[name]
            if kind=='skin':labels[owned&wool]=0
            elif kind=='wool':labels[owned&skin]=0
        # The head's front face contains eyes/blush; don't classify those as
        # separate materials here. They are geometrical expressions on the face.
        face=filled(labels==ids['face'])&opaque
        labels[face]=ids['face']
        for name,outlines in POSE_OUTLINES.get(action,{}).items():
            if index+1 in outlines:
                region=polygon(outlines[index+1])&opaque
                if kinds.get(name)=='skin':region&=skin|(r<120)
                labels[region]=ids[name]
        for _ in range(12):
            old=labels.copy()
            for name,kind in kinds.items():
                fringe=dilation(old==ids[name])&opaque&(old==0)
                if kind=='skin':fringe&=skin|(r<145)
                elif kind=='wool':fringe&=wool|(r<145)
                labels[fringe]=ids[name]
            if np.array_equal(old,labels):break
        # Retain original optical ownership for ambiguous pixels; this avoids
        # losing small coloured props and motion strokes.
        unknown=opaque&(labels==0)
        labels[unknown]=base[sy,sx][unknown]
        for _ in range(40):
            old=labels.copy()
            for name in ids:
                labels[opaque&(old==0)&dilation(old==ids[name])]=ids[name]
            if np.array_equal(old,labels):break
        labels[opaque&(labels==0)]=ids['accent']
        frames.append(labels)
    return frames


def make_rope(motions,morphs):
    # One continuous cable with stable ring correspondences. Unlike coloured
    # facial cells, this topology can interpolate without dissolving into dots.
    segments=96;targets=[];faces=[];cable_length=0
    for index,path in enumerate(ROPE_PATHS):
        points=np.asarray(path,dtype=float)
        lengths=np.linalg.norm(np.diff(points,axis=0),axis=1)
        cumulative=np.r_[0,np.cumsum(lengths)]
        t=np.linspace(0,cumulative[-1],segments+1)
        x=np.interp(t,cumulative,points[:,0]);y=np.interp(t,cumulative,points[:,1])
        cable_length=max(cable_length,float(cumulative[-1]))
        # Underfoot and behind-head phases have most of the cable length in
        # depth; match the authored projection without stretching the rope.
        amplitude=0.
        if index in (2,3,4,5,6):
            low,high=0.,400.
            for _ in range(30):
                amplitude=(low+high)/2
                depth=np.sin(np.linspace(0,math.pi,segments+1))*amplitude
                length=np.linalg.norm(np.diff(np.c_[x,y,depth],axis=0),axis=1).sum()
                if length<cable_length:low=amplitude
                else:high=amplitude
        depth=np.sin(np.linspace(0,math.pi,segments+1))*amplitude
        # Behind the head at frame six, in front of the body underfoot.
        if index in (2,3,4,6):depth=-depth
        center=np.c_[x,y,-46+depth]
        values=[]
        for i,(cx,cy,cz) in enumerate(center):
            tangent=center[min(i+1,segments),:2]-center[max(0,i-1),:2]
            tangent/=max(1e-8,np.linalg.norm(tangent));nx,ny=-tangent[1],tangent[0]
            for dz,radial in [(-3,-2),(-3,2),(-1,4),(2,4),(3,2),(3,-2),(2,-4),(-1,-4)]:
                values.append(vox.coord(cx+nx*radial,cy+ny*radial,cz+dz))
        targets.append(values)
    for i in range(segments):
        for j in range(8):faces.append((8*i+j,8*i+(j+1)%8,8*(i+1)+(j+1)%8,8*(i+1)+j))
    faces.extend([tuple(reversed(range(8))),tuple(segments*8+j for j in range(8))])
    mesh=bpy.data.meshes.new('rope');mesh.from_pydata(targets[0],[],faces);mesh.update()
    bm=bmesh.new();bm.from_mesh(mesh);bmesh.ops.recalc_face_normals(bm,faces=list(bm.faces));bm.to_mesh(mesh);bm.free()
    ob=bpy.data.objects.new('rope',mesh);bpy.context.collection.objects.link(ob);mesh.materials.append(vox.material)
    attr=mesh.color_attributes.new(name='Color',type='FLOAT_COLOR',domain='CORNER')
    for p in mesh.polygons:
        color=[244,235,207] if p.index%8 in (0,4) else [29,27,31]
        for loop in p.loop_indices:attr.data[loop].color=vox.srgb_linear(color)
    ob['rig_bone']='rope';ob['anatomy']='continuous rotating cable'
    ob.shape_key_add(name='Basis')
    for index in range(1,9):
        key=ob.shape_key_add(name=f'CablePose_{index+1}')
        key.data.foreach_set('co',np.asarray(targets[index],dtype=np.float32).ravel())
        morphs.append((key,index))
    vox.meshes.append(ob);motions['rope']=[(0,0)]*9
    return Vector(vox.coord(204,296,-46))


def build(action):
    destination=output/action;destination.mkdir(parents=True,exist_ok=True)
    bpy.ops.object.select_all(action='SELECT');bpy.ops.object.delete(use_global=False)
    vox.meshes=[];vox.stats=[]
    spec=SPECS[action];raw,colors,palette,references=read_images(action)
    # Several source PNGs have transparent holes inside the wool (crush,
    # pants and toy late poses). Keep the external silhouette, but complete
    # those accidental internal cutouts as hidden/reconstructed anatomy.
    poses.coverage=np.asarray([filled(frame[:,:,3]>.52) for frame in raw])
    motion_file=repo/'target/native/pet3d-fidelity/motion-fit'/f'{action}.npz'
    measured=None
    if not motion_file.exists():raise ValueError('Missing measured motion; run the pinned offline motion preparation first: '+action)
    metadata=json.loads(motion_file.with_suffix('.json').read_text())
    if metadata['references']!=[r['sha256'] for r in references]:raise ValueError('Motion input no longer matches source frames: '+action)
    with np.load(motion_file) as saved:
        measured={name:saved[name] for name in ['flow','backward','confidence']}
    vox.rgb=colors[0].copy();vox.source_name=references[0]['file']
    material=bpy.data.materials.new('Original pixel colours');material.use_nodes=True
    attr=material.node_tree.nodes.new('ShaderNodeVertexColor');attr.layer_name='Color'
    shader=material.node_tree.nodes.get('Principled BSDF');shader.inputs['Roughness'].default_value=1
    material.node_tree.links.new(attr.outputs['Color'],shader.inputs['Base Color']);vox.material=material
    opaque=raw[0,:,:,3]>.52
    if spec.get('exclude'):opaque&=~polygon(spec['exclude'])
    if spec.get('rope'):opaque=rope_character(opaque)
    names=['body','hood','face']+[p['name'] for p in spec['parts']]
    label_ids={n:i+1 for i,n in enumerate(names)}
    labels=np.zeros(SHAPE,dtype=np.int16)
    body_poly=polygon(spec['body']);head_poly=polygon(spec['head']);face_poly=polygon(spec['face'])
    labels[opaque&body_poly]=label_ids['body']
    labels[opaque&head_poly]=label_ids['hood']
    labels[opaque&face_poly]=label_ids['face']
    for p in spec['parts']:labels[opaque&polygon(p['points'])]=label_ids[p['name']]
    if spec.get('rope'):
        # Keep the character's head silhouette clear of the cable. The cable
        # is rebuilt as one articulated volume with a depth-aware cross-section.
        head_poly=polygon([(109,163),(136,149),(150,134),(177,128),(196,111),(214,111),(226,127),(247,132),(275,150),(296,169),(301,203),(297,240),(283,258),(257,270),(143,271),(119,256),(110,237)])
        keep=head_poly|body_poly|np.isin(labels,[label_ids[p['name']] for p in spec['parts']])
        opaque&=keep
        labels[~keep]=0
    # Assign small antialiased outline fringes to their nearest authored part.
    for _ in range(4):
        previous=labels.copy()
        for name in names:
            labels[opaque&(previous==0)&dilation(previous==label_ids[name])]=label_ids[name]
    unknown=opaque&(labels==0)
    # Isolated speed strokes remain small 3D effects, never a frame-sized mesh.
    names.append('accent');label_ids['accent']=len(label_ids)+1;labels[unknown]=label_ids['accent']
    labels_by_pose=pose_labels(action,spec,labels,label_ids,raw,colors,measured)

    head_track=tracked(raw,(labels==label_ids['hood'])&(Y<np.mean(np.asarray(spec['face'])[:,1])),spec.get('headMotionMax',32))
    motions={'root':[(0,0)]*9,'pelvis':head_track,'spine':head_track,'head':head_track}
    parents={'root':None,'pelvis':'root','spine':'pelvis','head':'spine'}
    for p in spec['parts']:
        name=p['name'];parents[name]=p['parent']
        if p['parent']=='head':motions[name]=head_track
        elif p['parent']=='root':motions[name]=[(0,0)]*9
        elif name in spec.get('motion',{}):
            offset=spec['motion'][name];motions[name]=[(head_track[i][0]+v[0],head_track[i][1]+v[1]) for i,v in enumerate(offset)]
        else:
            partmask=labels==label_ids[name]
            local=tracked(raw,partmask,32)
            motions[name]=local
    if action=='run':
        motions['foot.L']=[(0,0),(13,20),(9,0),(20,-5),(10,-4),(22,0),(16,1),(18,-2),(0,0)]
        motions['foot.R']=[(0,0),(-6,-8),(-5,-12),(-5,-9),(9,-2),(-2,-9),(-2,-6),(-2,-6),(0,0)]
    if action=='rope':
        jump=[0,-18,-24,-25,-23,-14,-9,-19,0]
        for name in list(motions):
            if name!='root':motions[name]=[(0,v) for v in jump]

    transforms={name:[np.array([[1,0,dx],[0,1,dy],[0,0,1]],dtype=float) for dx,dy in track] for name,track in motions.items()}
    if measured is not None:
        head_transforms=fit_transforms(measured['flow'],measured['confidence'],labels==label_ids['hood'])
        transforms['head']=head_transforms
        for name in ['pelvis','spine']:
            transforms[name]=fit_transforms(measured['flow'],measured['confidence'],labels==label_ids['body'])
        for p in spec['parts']:
            name=p['name']
            if p['parent']=='head':transforms[name]=head_transforms
            elif p['parent']=='root':transforms[name]=fit_transforms(measured['flow'],measured['confidence'],labels==label_ids[name])
            else:transforms[name]=fit_transforms(measured['flow'],measured['confidence'],labels==label_ids[name])

    # Each source pose owns explicit anatomy masks. Their closed 3D volumes
    # retain colour and silhouette while the skeleton supplies the fitted motion.
    # This avoids gaps caused by moving a frame-one torso with the head.
    morphs=[]
    face_names=['face','eye.L','eye.R','mouth']
    feature_regions=[('eye.L',spec['eyes'][0]),('eye.R',spec['eyes'][1]),('mouth',spec['mouth'])]
    for name,box in feature_regions:
        label_ids[name]=len(label_ids)+1
        parents[name]='head';motions[name]=head_track;transforms[name]=transforms['head']
    for index,pose in enumerate(labels_by_pose):
        inverse=np.linalg.inv(transforms['head'][index])
        local_x=inverse[0,0]*X+inverse[0,1]*Y+inverse[0,2]
        local_y=inverse[1,0]*X+inverse[1,1]*Y+inverse[1,2]
        c=colors[index].astype(float);r,g,b=c[:,:,0],c[:,:,1],c[:,:,2]
        dark=(r<165)&(g<150)&(b<145)
        red=(r>g*1.25)&(g<155)&(b<155)
        bright=(r>216)&(g>216)&(b>205)
        gold=(r>180)&(g>130)&(g<220)&(b<105)
        for name,(left,top,right,bottom) in feature_regions:
            region=(local_x>=left)&(local_x<right)&(local_y>=top)&(local_y<bottom)
            shape=region&(pose==label_ids['face'])&(dark|red|bright|gold)
            pose[shape]=label_ids[name]
    hands=[label_ids[p['name']] for p in spec['parts'] if p['name'].startswith('hand.')]
    face_masks=[];head_masks=[]
    for index,pose in enumerate(labels_by_pose):
        # Hands in front of the cheeks must not punch holes through the back
        # of the head. Complete the occluded face/hood within its authored
        # anatomical boundary; foreground hand ownership remains unchanged.
        hidden=sample_transform(face_poly,np.linalg.inv(transforms['head'][index]))&np.isin(pose,hands)
        face=filled(np.isin(pose,[label_ids[n] for n in face_names])|hidden)
        face_masks.append(face)
        head_masks.append(filled(np.isin(pose,[label_ids[n] for n in ['hood',*face_names]])|face))
    face_fronts=[]
    for index,mask in enumerate(face_masks):
        rows,cols=np.where(mask)
        cx=float(np.mean(X[rows,cols]));cy=float(np.mean(Y[rows,cols]))
        face_fronts.append(-58+18*((X-cx)/95)**2+12*((Y-cy)/80)**2)
    head_depths=[distance(mask) for mask in head_masks]
    poses.build('hood',head_masks,[pose==label_ids['hood'] for pose in labels_by_pose],colors,palette,
        [-22*(1-np.exp(-d/20)) for d in head_depths],
        [16+94*np.sqrt(np.minimum(1,d/72)) for d in head_depths],
        PALETTE['wool'],transforms['head'],'head',morphs)
    poses.build('face',face_masks,[pose==label_ids['face'] for pose in labels_by_pose],colors,palette,
        face_fronts,[np.full(SHAPE,20)]*9,PALETTE['skin'],transforms['head'],'head',morphs)
    for name,_ in feature_regions:
        masks=[pose==label_ids[name] for pose in labels_by_pose]
        poses.build(name,masks,masks,colors,palette,
            [np.floor(front/8)*8-8 for front in face_fronts],
            [np.floor(front/8)*8 for front in face_fronts],
            PALETTE['skin'],transforms['head'],name,morphs)
    body_masks=[filled(pose==label_ids['body']) for pose in labels_by_pose]
    for index,mask in enumerate(body_masks):
        hidden=sample_transform(body_poly,np.linalg.inv(transforms['spine'][index]))&np.isin(labels_by_pose[index],hands)
        mask=filled(mask|hidden)
        overlap=mask.copy()
        for _ in range(5):overlap=dilation(overlap)
        body_masks[index]=mask|(overlap&face_masks[index])
    # Complete hidden anatomy only within its measured silhouette. Visible
    # pixels always remain owned by the foreground hand/book/other prop.
    body_depths=[distance(mask) for mask in body_masks]
    poses.build('body',body_masks,[pose==label_ids['body'] for pose in labels_by_pose],colors,palette,
        [np.where(pose==label_ids['body'],-16-15*np.minimum(1,d/26),16) for pose,d in zip(labels_by_pose,body_depths)],
        [32+38*np.sqrt(np.minimum(1,d/36)) for d in body_depths],
        PALETTE['wool'],transforms['spine'],'spine',morphs)
    for p in spec['parts']:
        name=p['name'];visible=[pose==label_ids[name] for pose in labels_by_pose]
        masks=[filled(mask) for mask in visible]
        depths=[distance(mask) for mask in masks]
        bulges=[min(12,p['depth']/4)*np.minimum(1,d/14) for d in depths]
        fronts=[p['front']-bulge for bulge in bulges]
        backs=[p['front']+p['depth']+bulge*.4 for bulge in bulges]
        if p['kind'] in ('book','paper','dark','car','gold','blue','pink'):
            fronts=[p['front']]*9;backs=[p['front']+p['depth']]*9
        poses.build(name,masks,visible,colors,palette,fronts,backs,
            PALETTE[p['kind']],transforms[name],name,morphs)
    accents=[pose==label_ids['accent'] for pose in labels_by_pose]
    poses.build('accent',accents,accents,colors,palette,[-26]*9,[-18]*9,[160,160,156],
        transforms['root'],'root',morphs)
    rope_center=make_rope(motions,morphs) if spec.get('rope') else None
    if rope_center is not None:parents['rope']='root'

    # A stable bone-name contract is shared by every action, with prop bones
    # added to that hierarchy. All exported mesh vertices carry skin weights.
    for name,parent in [('hand.L','spine'),('hand.R','spine'),('foot.L','pelvis'),('foot.R','pelvis'),('ear.L','head'),('ear.R','head'),('nose','head'),('tail','pelvis')]:
        parents.setdefault(name,parent);motions.setdefault(name,head_track)
        transforms.setdefault(name,transforms['head'])
    armature=bpy.data.armatures.new('Libby action skeleton');rig=bpy.data.objects.new('LibbyRig',armature);bpy.context.collection.objects.link(rig)
    bpy.ops.object.select_all(action='DESELECT');rig.select_set(True);bpy.context.view_layer.objects.active=rig;bpy.ops.object.mode_set(mode='EDIT')
    pending=dict(parents)
    while pending:
        progress=False
        for name,parent in list(pending.items()):
            if parent and parent not in armature.edit_bones:continue
            bone=armature.edit_bones.new(name)
            objects=[o for o in vox.meshes if o['rig_bone']==name]
            if objects:
                points=[v.co for o in objects for v in o.data.vertices]
                center=sum(points,Vector())/len(points)
            else:center=Vector(vox.coord(209,280 if name=='head' else 330,0))
            if name=='rope':center=rope_center
            bone.head=center;bone.tail=center+Vector((0,0,.2))
            if parent:bone.parent=armature.edit_bones[parent]
            del pending[name];progress=True
        if not progress:raise ValueError('Bone cycle: '+str(pending))
    bpy.ops.object.mode_set(mode='OBJECT');rig.show_in_front=True
    for ob in vox.meshes:
        group=ob.vertex_groups.new(name=ob['rig_bone']);group.add(list(range(len(ob.data.vertices))),1,'REPLACE')
        modifier=ob.modifiers.new('Original action skeletal skin','ARMATURE');modifier.object=rig
    scene=bpy.context.scene;scene.render.fps=30;scene.frame_start=1;scene.frame_end=82
    rig.animation_data_create();rig.animation_data.action=bpy.data.actions.new(action)
    for index in range(10):
        frame=1+index*9;key=index%9
        for name,parent in parents.items():
            bone=rig.pose.bones[name];dx,dy=motions.get(name,[(0,0)]*9)[key]
            px,py=motions.get(parent,[(0,0)]*9)[key] if parent else (0,0)
            world_offset=Vector(((dx-px)*.01,0,-(dy-py)*.01))
            bone.location=bone.bone.matrix_local.to_3x3().inverted()@world_offset
            bone.rotation_mode='XYZ';bone.rotation_euler=(0,0,0)
            if name in transforms:
                bone.matrix=world_transform(transforms[name][key])@bone.bone.matrix_local
                bpy.context.view_layer.update()
            bone.keyframe_insert('location',frame=frame);bone.keyframe_insert('rotation_euler',frame=frame);bone.keyframe_insert('scale',frame=frame)
        for target,expression in morphs:target.value=float(key==expression);target.keyframe_insert('value',frame=frame)
    # A corrective voxel pose and its bones form one assembly. Advancing only
    # the bones while holding the previous shape opens neck/limb/prop seams;
    # independently interpolating coloured prisms breaks the volume apart.
    # Keep the original nine-pose cadence and change the complete assembly at
    # the same instant, including the cable and the hands holding its handles.
    animated=[rig]+[ob.data.shape_keys for ob in vox.meshes if ob.data.shape_keys]
    for owner in animated:
        if not owner.animation_data or not owner.animation_data.action:continue
        action_data=owner.animation_data.action
        slot=owner.animation_data.action_slot
        for layer in action_data.layers:
            for strip in layer.strips:
                bag=strip.channelbag(slot)
                if bag:
                    for curve in bag.fcurves:
                        for keyframe in curve.keyframe_points:keyframe.interpolation='CONSTANT'
    scene.frame_set(1)
    scene['asset_status']='pixel-action-review-required';scene['action']=action
    scene['pose_timing']='synchronized-pixel-poses'
    scene['source']='nine original frames; semantic volume mesh; skeletal motion and facial voxel morphs'
    scene['unverified']='Unseen sides/back inferred; exact motion/contact and likeness require visual acceptance.'
    scene.render.engine='BLENDER_WORKBENCH';scene.display.shading.light='FLAT';scene.display.shading.color_type='VERTEX'
    scene.display.shading.show_shadows=False;scene.display.shading.show_cavity=False;scene.display.shading.show_backface_culling=True
    scene.display.render_aa='16';scene.view_settings.view_transform='Standard';scene.render.film_transparent=True
    scene.render.resolution_x=418;scene.render.resolution_y=418;scene.render.resolution_percentage=100
    scene.render.image_settings.file_format='PNG';scene.render.image_settings.color_mode='RGBA'
    bpy.ops.object.camera_add();camera=bpy.context.object;camera.data.type='ORTHO';camera.data.ortho_scale=4.18;scene.camera=camera
    target=Vector((0,.2,1.6))
    def angle(degrees):
        a=math.radians(degrees);camera.location=(math.sin(a)*8,.2-math.cos(a)*8,1.6);camera.rotation_euler=(target-camera.location).to_track_quat('-Z','Y').to_euler()
    angle(0)
    bpy.ops.wm.save_as_mainfile(filepath=str(destination/(action+'.blend')),compress=True)
    bpy.ops.object.select_all(action='DESELECT')
    for ob in vox.meshes+[rig]:ob.select_set(True)
    bpy.context.view_layer.objects.active=rig
    bpy.ops.export_scene.gltf(filepath=str(destination/(action+'.glb')),export_format='GLB',use_selection=True,export_animations=True,export_animation_mode='SCENE',export_anim_scene_split_object=False,export_skins=True,export_morph=True,export_morph_normal=False,export_materials='EXPORT',export_extras=True,export_vertex_color='MATERIAL',export_force_sampling=True,export_frame_range=True)
    # Pack and synchronize the GLB itself, so any compliant viewer uses the
    # same timing rather than relying on a preview-only workaround.
    packing=pack_glb(destination/(action+'.glb'))
    parts=[]
    for ob in vox.meshes:
        ob.data.calc_loop_triangles();bm=bmesh.new();bm.from_mesh(ob.data)
        parts.append({'name':ob.name,'vertices':len(ob.data.vertices),'triangles':len(ob.data.loop_triangles),'boundaryEdges':sum(e.is_boundary for e in bm.edges),'nonmanifoldEdges':sum(not e.is_manifold for e in bm.edges),'signedVolume':bm.calc_volume(signed=True)})
        bm.free()
    audit={'action':action,'title':spec['title'],'status':'pixel-action-review-required','references':references,'parts':parts,'triangles':sum(p['triangles'] for p in parts),'bones':len(armature.bones),'motion':motions,'packing':packing,'completed':False}
    (destination/'audit.json').write_text(json.dumps(audit,ensure_ascii=False,indent=2)+'\n')
    if not args.no_render:
        for index in range(9):
            scene.frame_set(1+index*9);scene.render.filepath=str(destination/f'frame-{index+1:02}.png');bpy.ops.render.render(write_still=True)
        scene.frame_set(1)
        for label,degrees in [('quarter',30),('back',180)]:
            angle(degrees);scene.render.filepath=str(destination/(label+'.png'));bpy.ops.render.render(write_still=True)
    print(json.dumps({'action':action,'triangles':audit['triangles'],'bones':audit['bones'],'meshCount':len(parts),'geometryIssues':[p for p in parts if p['boundaryEdges'] or p['nonmanifoldEdges'] or p['signedVolume']<=0]}),flush=True)


for action in ([args.action] if args.action else SPECS):build(action)
