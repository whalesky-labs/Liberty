"""Build Libby's reference-constrained, editable voxel character in Blender 4.5.

Usage: blender --background --factory-startup --python <this file> -- REPO OUT

The source drawing supplies visible silhouette and vertex colours. Separate
semantic solids supply depth; no bitmap is exported on a card or a billboard.
Rear surfaces and hidden anatomy are inferred, and require visual review.
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
from mathutils import Vector


parser = argparse.ArgumentParser()
parser.add_argument('repo', type=Path)
parser.add_argument('output', type=Path)
parser.add_argument('--no-render', action='store_true')
parser.add_argument('--animate-review', action='store_true')
args = parser.parse_args(sys.argv[sys.argv.index('--') + 1:])
repo, output = args.repo.resolve(), args.output.resolve()
output.mkdir(parents=True, exist_ok=True)
bpy.ops.object.select_all(action='SELECT')
bpy.ops.object.delete(use_global=False)

S = .01
CELL = 2
DEPTH = 8
WIDTH = HEIGHT = 418
SHAPE = (HEIGHT // CELL, WIDTH // CELL)
yy, xx = np.indices(SHAPE)
X, Y = xx * CELL + CELL / 2, yy * CELL + CELL / 2
SOURCE = repo / 'packages/desktop-resources/images/pet/action/crush/crush_01.png'
im = bpy.data.images.load(str(SOURCE), check_existing=False)
im.colorspace_settings.name = 'Non-Color'
pixels = np.empty(WIDTH * HEIGHT * 4, dtype=np.float32)
im.pixels.foreach_get(pixels)
pixels = pixels.reshape(HEIGHT, WIDTH, 4)[::-1].copy()
# Preserve hard edge positions; antialiased outer pixels are not new voxels.
samples = pixels.reshape(SHAPE[0], CELL, SHAPE[1], CELL, 4).mean(axis=(1, 3))
opaque = samples[:, :, 3] >= .52
rgb = np.clip(np.round(samples[:, :, :3] * 255 / 4) * 4, 0, 255).astype(np.uint8)
# A fixed, small palette keeps front faces mergeable without blurring edges.
# Deterministic weighted k-means operates only on opaque reference colours.
values, counts = np.unique(rgb[opaque].reshape(-1, 3), axis=0, return_counts=True)
values = values.astype(np.float64)
centers = [values[np.argmax(counts)]]
nearest = np.full(len(values), np.inf)
for _ in range(63):
    nearest = np.minimum(nearest, ((values-centers[-1])**2).sum(axis=1))
    centers.append(values[np.argmax(nearest*np.sqrt(counts))])
centers = np.asarray(centers)
for _ in range(12):
    assignment = ((values[:,None,:]-centers[None,:,:])**2).sum(axis=2).argmin(axis=1)
    for i in range(len(centers)):
        selected = assignment == i
        if selected.any():
            centers[i] = np.average(values[selected],axis=0,weights=counts[selected])
palette = np.clip(np.round(centers),0,255).astype(np.uint8)
flat = rgb.reshape(-1,3).astype(np.float64)
rgb = palette[((flat[:,None,:]-centers[None,:,:])**2).sum(axis=2).argmin(axis=1)].reshape(*SHAPE,3)
bpy.data.images.remove(im)


def polygon(points):
    inside = np.zeros(SHAPE, dtype=bool)
    x0, y0 = points[-1]
    for x1, y1 in points:
        if y0 != y1:
            inside ^= ((y0 > Y) != (y1 > Y)) & (X < (x1 - x0) * (Y - y0) / (y1 - y0) + x0)
        x0, y0 = x1, y1
    return inside


def dilate(mask, count=1):
    for _ in range(count):
        p = np.pad(mask, 1)
        mask = p[1:-1, 1:-1] | p[:-2, 1:-1] | p[2:, 1:-1] | p[1:-1, :-2] | p[1:-1, 2:]
    return mask


def fill_holes(mask):
    outside = np.zeros_like(mask)
    queue = deque([(0, 0)])
    while queue:
        y, x = queue.popleft()
        if not (0 <= y < SHAPE[0] and 0 <= x < SHAPE[1]) or mask[y, x] or outside[y, x]:
            continue
        outside[y, x] = True
        queue.extend([(y - 1, x), (y + 1, x), (y, x - 1), (y, x + 1)])
    return ~outside


def inward_distance(mask):
    result = np.zeros(SHAPE, dtype=np.float32)
    layer = mask.copy()
    while layer.any():
        result[layer] += CELL
        p = np.pad(layer, 1)
        layer = p[1:-1, 1:-1] & p[:-2, 1:-1] & p[2:, 1:-1] & p[1:-1, :-2] & p[1:-1, 2:]
    return result


# These boundaries separate visible parts, including their dark pixel outlines.
# Coordinates are authored in the source image, not guessed in world units.
outlines = {
    'hand.L': [(88,245),(97,238),(109,239),(117,248),(123,252),(131,258),(139,266),(146,276),(148,294),(141,313),(130,318),(113,318),(103,312),(102,295),(104,284),(97,277),(90,267)],
    'hand.R': [(201,276),(211,262),(226,254),(232,243),(248,239),(262,242),(270,251),(270,266),(263,278),(250,285),(259,284),(270,283),(281,290),(285,300),(279,311),(265,317),(247,322),(226,320),(210,313),(203,301)],
    'horn.L': [(62,67),(104,66),(105,77),(119,78),(125,90),(124,106),(110,115),(108,129),(89,131),(80,112),(72,108),(69,96),(62,94)],
    'horn.R': [(276,90),(300,78),(324,77),(328,66),(365,66),(374,75),(371,95),(347,105),(342,116),(333,118),(334,135),(306,138),(297,127),(279,121)],
    'ear.L': [(42,160),(60,158),(64,149),(76,151),(76,186),(52,189),(43,180)],
    'ear.R': [(305,154),(331,154),(340,162),(351,165),(354,171),(372,171),(378,181),(375,193),(365,199),(338,199),(330,189),(312,188),(306,179)],
    'tail': [(278,289),(294,291),(307,305),(310,322),(303,333),(280,335)],
    'foot.L': [(127,331),(179,330),(184,350),(181,371),(131,371),(126,356)],
    'foot.R': [(237,332),(290,326),(296,347),(288,372),(239,372)],
    'face': [(90,251),(91,215),(96,205),(96,173),(105,162),(118,151),(136,150),(140,143),(154,145),(159,154),(200,154),(207,144),(220,147),(225,158),(242,160),(256,171),(261,189),(274,198),(273,233),(261,239),(260,254),(243,274),(222,286),(176,286),(146,281),(125,273),(107,264)],
}
part_names = ['hood', 'body', 'face', 'foot.L', 'foot.R', 'tail', 'horn.L', 'horn.R', 'ear.L', 'ear.R', 'hand.L', 'hand.R', 'eye.L', 'eye.R', 'nose', 'mouth']
ids = {name: i + 1 for i, name in enumerate(part_names)}
labels = np.zeros(SHAPE, dtype=np.int16)
labels[opaque] = ids['hood']
labels[opaque & (Y > 280)] = ids['body']
for name in ['face', 'foot.L', 'foot.R', 'tail', 'horn.L', 'horn.R', 'ear.L', 'ear.R', 'hand.L', 'hand.R']:
    labels[opaque & polygon(outlines[name])] = ids[name]
# Embedded eyes/nose/mouth become separate, solid facial rig pieces.
for name, box in [('eye.L', (113,148,182,218)), ('eye.R', (194,229,182,219)), ('nose',(147,180,207,229)), ('mouth',(146,184,236,252))]:
    left, right, top, bottom = box
    feature = (X >= left) & (X < right) & (Y >= top) & (Y < bottom)
    ink = rgb[:, :, 0] < 160
    feature &= dilate(ink & feature, 1)
    feature = fill_holes(feature)
    labels[feature & (labels == ids['face'])] = ids[name]


def coord(x, y, depth):
    return ((x - 209) * S, depth * S, (369 - y) * S)


def srgb_linear(color):
    v = np.asarray(color, dtype=np.float64) / 255
    return tuple(np.where(v <= .04045, v / 12.92, ((v + .055) / 1.055) ** 2.4)) + (1,)


material = bpy.data.materials.new('Libby pixel colour')
material.use_nodes = True
nodes, links = material.node_tree.nodes, material.node_tree.links
vertex_color = nodes.new('ShaderNodeVertexColor')
vertex_color.layer_name = 'Color'
bsdf = nodes.get('Principled BSDF')
links.new(vertex_color.outputs['Color'], bsdf.inputs['Base Color'])
bsdf.inputs['Roughness'].default_value = 1

meshes = []
stats = []


def volume(name, mask, front, back, base, visible=None, bone=None):
    """Closed union of depth voxels, with shared vertices and no internal faces."""
    mask = mask.copy()
    # Remove only diagonal, zero-area contacts in a silhouette. A 2 px bridge
    # gives each contact finite volume and avoids nonmanifold voxel edges.
    for _ in range(3):
        a,bmask,cmask,dmask=mask[:-1,:-1],mask[:-1,1:],mask[1:,:-1],mask[1:,1:]
        bad=(a & dmask & ~bmask & ~cmask) | (bmask & cmask & ~a & ~dmask)
        rows,cols=np.where(bad)
        if not len(rows):break
        for row,col in zip(rows,cols):
            if mask[row,col]:mask[row,col+1]=True
            else:mask[row,col]=True
    if visible is None:
        visible = labels == ids[name]
    front = np.broadcast_to(front, SHAPE)
    back = np.broadcast_to(back, SHAPE)
    f = np.floor(front / DEPTH).astype(np.int16)
    b = np.maximum(f + 1, np.ceil(back / DEPTH).astype(np.int16))
    ys, xs = np.where(mask)
    if not len(xs):
        raise ValueError('Empty component: ' + name)
    min_x, max_x, min_y, max_y = xs.min(), xs.max(), ys.min(), ys.max()
    min_d, max_d = f[mask].min(), b[mask].max()
    # Volume axes are image row, image column, depth. Six exposed grid faces
    # are emitted; all internal voxel faces are discarded before export.
    d = np.arange(min_d, max_d)
    occupied = mask[min_y:max_y + 1, min_x:max_x + 1, None] & (d >= f[min_y:max_y + 1, min_x:max_x + 1, None]) & (d < b[min_y:max_y + 1, min_x:max_x + 1, None])
    # Repair ambiguous diagonal contacts on every 3D grid plane. This adds a
    # one-cell bridge rather than exporting edges shared by four surfaces.
    for _ in range(6):
        changed=0
        for order in [(0,1,2),(0,2,1),(1,2,0)]:
            grid=occupied.transpose(order)
            a,bmask,cmask,dmask=grid[:-1,:-1,:],grid[:-1,1:,:],grid[1:,:-1,:],grid[1:,1:,:]
            bad=a&dmask&~bmask&~cmask
            changed+=int(bad.sum())
            bmask[bad]=True
            bad=bmask&cmask&~a&~dmask
            changed+=int(bad.sum())
            a[bad]=True
        if not changed:break
    p = np.pad(occupied, 1)
    faces, colors, vertices, vertex_map = [], [], [], {}
    base = np.array(base)
    directions = [
        ((0,0,-1), p[1:-1,1:-1,:-2], [(0,0,0),(1,0,0),(1,1,0),(0,1,0)]),
        ((0,0,1), p[1:-1,1:-1,2:], [(0,1,1),(1,1,1),(1,0,1),(0,0,1)]),
        ((0,-1,0), p[1:-1,:-2,1:-1], [(0,0,1),(0,0,0),(0,1,0),(0,1,1)]),
        ((0,1,0), p[1:-1,2:,1:-1], [(1,0,0),(1,0,1),(1,1,1),(1,1,0)]),
        ((-1,0,0), p[:-2,1:-1,1:-1], [(0,0,1),(1,0,1),(1,0,0),(0,0,0)]),
        ((1,0,0), p[2:,1:-1,1:-1], [(0,1,0),(1,1,0),(1,1,1),(0,1,1)]),
    ]
    for normal, neighbor, corners in directions:
        for ly, lx, ld in zip(*np.where(occupied & ~neighbor)):
            y, x, depth = int(ly + min_y), int(lx + min_x), int(ld + min_d)
            indices = []
            for ox, oy, od in corners:
                key = (x + ox, y + oy, depth + od)
                index = vertex_map.get(key)
                if index is None:
                    index = len(vertices)
                    vertex_map[key] = index
                    vertices.append(coord(key[0] * CELL, key[1] * CELL, key[2] * DEPTH))
                indices.append(index)
            faces.append(list(reversed(indices)))
            if normal == (0,0,-1) and visible[y,x]:
                color = rgb[y,x]
            else:
                # Rear/side colour has no projected face or hand imagery.
                # Voxel planes receive modest directional shading at runtime.
                color = base.copy()
                if name in ('hood', 'body', 'tail') and depth >= 5:
                    shade = 0 if ((x // 8 + y // 8 + depth // 6) % 7) else -6
                    color = np.clip(color + shade, 0, 255)
                # The source contour ink wraps only the first voxel of depth.
                if visible[y,x] and depth == f[y,x]:
                    color = rgb[y,x]
            colors.append(tuple(int(v) for v in color))
    mesh = bpy.data.meshes.new(name)
    mesh.from_pydata(vertices, [], faces)
    mesh.update()
    attr = mesh.color_attributes.new(name='Color', type='FLOAT_COLOR', domain='CORNER')
    palette = {color: srgb_linear(color) for color in set(colors)}
    array = np.asarray([palette[color] for color in colors], dtype=np.float32)
    attr.data.foreach_set('color', np.repeat(array, 4, axis=0).ravel())
    # Dissolve only coplanar, identically coloured faces. Keep colour and
    # silhouette seams: this is topology reduction, not image smoothing.
    bm=bmesh.new()
    bm.from_mesh(mesh)
    bm.normal_update()
    color_layer=bm.loops.layers.float_color['Color']
    for edge in bm.edges:
        if len(edge.link_faces)!=2:
            edge.seam=True
        else:
            a,bface=edge.link_faces
            edge.seam=(a.loops[0][color_layer]-bface.loops[0][color_layer]).length>.0001
    bmesh.ops.dissolve_limit(bm,angle_limit=.001,use_dissolve_boundaries=False,verts=list(bm.verts),edges=list(bm.edges),delimit={'SEAM'})
    bmesh.ops.recalc_face_normals(bm,faces=list(bm.faces))
    bm.to_mesh(mesh)
    bm.free()
    mesh.update()
    mesh.calc_loop_triangles()
    ob = bpy.data.objects.new(name, mesh)
    bpy.context.collection.objects.link(ob)
    ob.data.materials.append(material)
    ob['anatomy'] = name
    ob['source'] = str(SOURCE.relative_to(repo))
    ob['depth_authority'] = 'inferred-volume; source fixes visible contour/colour'
    ob['rig_bone'] = bone or name
    meshes.append(ob)
    stats.append({'name': name, 'vertices': len(mesh.vertices), 'triangles': len(mesh.loop_triangles), 'depthPixels': int((max_d - min_d)*DEPTH)})
    print('Solid', name, len(mesh.vertices), len(mesh.loop_triangles), flush=True)
    return ob


head_visible = np.isin(labels, [ids[n] for n in ['hood','face','eye.L','eye.R','nose','mouth']])
head_mask = fill_holes(head_visible | (polygon(outlines['face']) & (Y < 284)))
distance = inward_distance(head_mask)
volume('hood', head_mask, -28*(1-np.exp(-distance/20)), 18+100*np.sqrt(np.minimum(1,distance/74)), [235,236,232], bone='head')

face_mask = fill_holes(np.isin(labels,[ids[n] for n in ['face','eye.L','eye.R','nose','mouth']]) | (polygon(outlines['face']) & np.isin(labels,[ids['hand.L'],ids['hand.R']]) ))
face_front = -64 + 24*((X-181)/91)**2 + 17*((Y-215)/72)**2
face_front = np.floor(face_front / DEPTH)*DEPTH
volume('face', face_mask, face_front, 4+20*np.minimum(1,inward_distance(face_mask)/28), [255,205,160], bone='head')

body_mask = fill_holes((labels == ids['body']) | polygon([(133,278),(248,278),(273,291),(286,318),(277,340),(248,354),(177,354),(142,339),(127,313)]))
body_mask &= ~np.isin(labels,[ids['foot.L'],ids['foot.R'],ids['tail']])
distance = inward_distance(body_mask)
volume('body', body_mask, -17-18*np.minimum(1,distance/26), 14+42*np.sqrt(np.minimum(1,distance/40)), [239,240,235], bone='spine')

for side,points in [('L',[(131,286),(147,286),(154,300),(143,309),(131,305)]),('R',[(255,286),(272,286),(278,303),(265,313),(251,303)])]:
    mask=polygon(points)
    volume('sleeve.'+side,mask,-34,18,[236,238,233],visible=np.zeros(SHAPE,dtype=bool),bone='upper_arm.'+side)

for side in ('L','R'):
    name = 'horn.'+side
    mask = labels == ids[name]
    distance = inward_distance(mask)
    volume(name,mask,10-13*np.minimum(1,distance/12),20+25*np.minimum(1,distance/16),[146,85,45],bone='head')
    name='ear.'+side
    mask=labels==ids[name]
    distance=inward_distance(mask)
    volume(name,mask,-38-8*np.minimum(1,distance/12),-15+12*np.minimum(1,distance/12),[244,188,139],bone=name)
    name='hand.'+side
    mask=fill_holes(labels==ids[name])
    distance=inward_distance(mask)
    volume(name,mask,-70-12*np.minimum(1,distance/14),-32+8*np.minimum(1,distance/14),[255,204,158],bone=name)
    name='foot.'+side
    mask=labels==ids[name]
    distance=inward_distance(mask)
    volume(name,mask,2-10*np.minimum(1,distance/12),25+10*np.minimum(1,distance/16),[157,92,50],bone=name)

distance=inward_distance(labels==ids['tail'])
volume('tail',labels==ids['tail'],12-8*np.minimum(1,distance/10),32+15*np.minimum(1,distance/10),[226,228,222],bone='tail')
for name in ['eye.L','eye.R','nose','mouth']:
    mask=labels==ids[name]
    inset=12 if name=='nose' else 8
    volume(name,mask,face_front-inset,face_front,[55,32,22] if name=='nose' else [35,25,17],bone=name)


def step_contour(points):
    """Axis-aligned contour with explicit stair corners, resampled per edge."""
    result=[]
    for i,(x,y) in enumerate(points):
        nx,ny=points[(i+1)%len(points)]
        result.append((x,y))
        if x!=nx and y!=ny:
            result.append((nx,y))
    return result


def contour_samples(points,count=128):
    # A constant topology across expressions allows actual mesh morph targets.
    lengths=[math.dist(p,points[(i+1)%len(points)]) for i,p in enumerate(points)]
    total=sum(lengths)
    result=[]
    edge=0
    base=0
    for i in range(count):
        position=i/count*total
        while edge<len(points)-1 and base+lengths[edge]<position:
            base+=lengths[edge]
            edge+=1
        t=(position-base)/max(.00001,lengths[edge])
        a,b=points[edge],points[(edge+1)%len(points)]
        result.append((a[0]*(1-t)+b[0]*t,a[1]*(1-t)+b[1]*t))
    return result


def expression_solid(name,points,cx,cy,color,bone,thickness=2,offset=8):
    """Closed colour solid, never a textured rectangle or frame replacement."""
    vertices=[]
    for depth_offset in (offset,offset-thickness):
        for x,y in points:
            px,py=cx+x,cy+y
            # Facial pieces are shallow solids with a planar cap; a concave
            # n-gon cannot describe a curved cap without explicit topology.
            d=-64+24*((cx-181)/91)**2+17*((cy-215)/72)**2-depth_offset
            vertices.append(coord(px,py,d))
    n=len(points)
    faces=[tuple(reversed(range(n))),tuple(range(n,2*n))]
    faces += [(i,(i+1)%n,(i+1)%n+n,i+n) for i in range(n)]
    mesh=bpy.data.meshes.new(name)
    mesh.from_pydata(vertices,[],faces)
    mesh.update()
    bm=bmesh.new();bm.from_mesh(mesh)
    bmesh.ops.recalc_face_normals(bm,faces=list(bm.faces))
    bm.to_mesh(mesh);bm.free()
    mesh.update()
    attr=mesh.color_attributes.new(name='Color',type='FLOAT_COLOR',domain='CORNER')
    for datum in attr.data:
        datum.color=srgb_linear(color)
    ob=bpy.data.objects.new(name,mesh)
    bpy.context.collection.objects.link(ob)
    mesh.materials.append(material)
    ob['rig_bone']=bone
    ob['anatomy']='facial expression solid'
    meshes.append(ob)
    return ob


# Geometric facial rig: one eye mesh deforms to the heart contour. Red irises,
# highlights and star emblems expand/retract as face components; there is no
# per-frame character mesh, texture atlas or source-frame visibility track.
expression_targets=[]
heart=step_contour([(-14,-8),(-9,-14),(-3,-14),(0,-10),(5,-14),(11,-14),(16,-8),(16,0),(11,7),(5,13),(0,16),(-6,11),(-11,6),(-16,0),(-16,-6)])
heart_inner=[(x*.78,y*.78) for x,y in heart]
star=step_contour([(0,-12),(4,-5),(11,-4),(11,1),(5,3),(3,11),(-2,11),(-4,4),(-11,2),(-11,-3),(-4,-5)])
for side,cx in [('L',131),('R',211)]:
    eye=bpy.data.objects['eye.'+side]
    eye.shape_key_add(name='Basis')
    heart_key=eye.shape_key_add(name='Heart')
    for vertex,target_vertex in zip(eye.data.vertices,heart_key.data):
        px=vertex.co.x/S+209-cx
        py=369-vertex.co.z/S-201
        angle=math.atan2(py,px)
        dx,dy=math.cos(angle),math.sin(angle)
        source_radius=1
        for radius in np.arange(.5,24,.5):
            row,col=int((201+dy*radius)/CELL),int((cx+dx*radius)/CELL)
            if labels[row,col]==ids['eye.'+side]:source_radius=radius+1
        crossings=[]
        for i,a in enumerate(heart):
            b=heart[(i+1)%len(heart)]
            sx,sy=b[0]-a[0],b[1]-a[1]
            den=dx*sy-dy*sx
            if abs(den)<1e-6:continue
            radius=(a[0]*sy-a[1]*sx)/den
            t=(a[0]*dy-a[1]*dx)/den
            if radius>0 and 0<=t<=1:crossings.append(radius)
        destination_radius=min(crossings) if crossings else 15
        factor=min(1,math.hypot(px,py)/source_radius)
        nx,ny=dx*destination_radius*factor,dy*destination_radius*factor
        target_vertex.co.x=(cx+nx-209)*S
        target_vertex.co.z=(369-201-ny)*S
    expression_targets.append((heart_key,[0,0,0,0,1,1,1,0,0,0]))
    for label,shape,color,weights in [
        ('heart_border',heart,[48,31,21],[0,0,0,0,1,1,1,0,0,0]),
        ('heart_iris',heart_inner,[239,108,91],[0,0,0,0,1,1,1,0,0,0]),
        ('star',star,[255,212,76],[0,0,0,1,0,0,0,0,0,0]),
        ('sparkle',[(-3,-3),(3,-3),(3,3),(-3,3)],[255,250,233],[0,1,1,0,0,0,0,1,0,0]),
    ]:
        ey=201 if label!='sparkle' else 209
        ex=cx if label!='sparkle' else cx+5
        ob=expression_solid(label+'.'+side,shape,ex,ey,color,'eye.'+side,offset=21 if label=='heart_border' else 23)
        full=[v.co.copy() for v in ob.data.vertices]
        # Store the resting component inside the opaque eye volume.
        center=Vector(coord(ex,ey,-48))
        for vertex in ob.data.vertices:
            vertex.co=center+(vertex.co-center)*.015
        ob.shape_key_add(name='Basis')
        target=ob.shape_key_add(name='Expand')
        for v,co in zip(target.data,full):
            v.co=co
        expression_targets.append((target,weights))
    glint=expression_solid('eye_glint.'+side,[(-3,-4),(4,-4),(4,2),(0,2),(0,5),(-3,5)],cx-5,194,[255,250,233],'eye.'+side,offset=26)
    # One highlight is shared across the ordinary and love expressions.
    glint['scope']='heart eye catchlight; ordinary eye colour comes from reference'
    full=[v.co.copy() for v in glint.data.vertices]
    center=Vector(coord(cx-7,195,-48))
    for v in glint.data.vertices:
        v.co=center+(v.co-center)*.015
    glint.shape_key_add(name='Basis')
    target=glint.shape_key_add(name='Expand')
    for v,co in zip(target.data,full):v.co=co
    expression_targets.append((target,[0,0,0,0,1,1,1,0,0,0]))
    for i,(dx,dy,radius) in enumerate([(-8,7,2),(7,-3,2),(-2,12,2)]):
        ob=expression_solid(f'tear_glint.{side}.{i}',[(-radius,-radius),(radius,-radius),(radius,radius),(-radius,radius)],cx+dx,201+dy,[255,250,233],'eye.'+side,offset=25)
        full=[v.co.copy() for v in ob.data.vertices]
        center=Vector(coord(cx+dx,201+dy,-40))
        for v in ob.data.vertices:v.co=center+(v.co-center)*.015
        ob.shape_key_add(name='Basis')
        key=ob.shape_key_add(name='Sparkle')
        for v,co in zip(key.data,full):v.co=co
        expression_targets.append((key,[0,1,1,0,0,0,0,1,0,0]))

# Mouth grows as a single open cavity and tongue; the smile stays at its rim.
mouth_points=step_contour([(-14,-4),(14,-4),(14,8),(11,8),(11,14),(7,14),(7,19),(-7,19),(-7,15),(-11,15),(-11,10),(-14,10)])
for label,shape,color,offset in [
    ('mouth_cavity',mouth_points,[79,35,22],20),
    ('tongue',[(x*.73,y*.60+5) for x,y in mouth_points],[230,114,89],24),
]:
    ob=expression_solid(label,shape,164,239,color,'mouth',offset=offset)
    full=[v.co.copy() for v in ob.data.vertices]
    for v in ob.data.vertices:
        v.co.z=(369-243)*S+(v.co.z-(369-243)*S)*.025
        v.co.y+=.09
    ob.shape_key_add(name='Basis')
    target=ob.shape_key_add(name='Open')
    for v,co in zip(target.data,full):v.co=co
    expression_targets.append((target,[0,0,.7,0,.9,1,.7,0,0,0]))

drool=expression_solid('drool',step_contour([(-1,-5),(1,-5),(1,1),(3,5),(2,8),(-2,8),(-3,5),(-1,1)]),179,251,[250,240,230],'mouth',offset=15)
full=[v.co.copy() for v in drool.data.vertices]
center=Vector(coord(179,251,-35))
for v in drool.data.vertices:v.co=center+(v.co-center)*.01
drool.shape_key_add(name='Basis')
key=drool.shape_key_add(name='Drool')
for v,co in zip(key.data,full):v.co=co
expression_targets.append((key,[0,0,0,1,0,0,0,0,0,0]))

# Real armature and vertex groups. Pixel solids have rigid weights, preserving
# square voxels; pivots animate bones, not mesh visibility or image sequences.
armature=bpy.data.armatures.new('Libby voxel skeleton')
rig=bpy.data.objects.new('LibbyRig',armature)
bpy.context.collection.objects.link(rig)
bpy.context.view_layer.objects.active=rig
rig.select_set(True)
bpy.ops.object.mode_set(mode='EDIT')
bone_specs=[
    ('root',(209,352,20),(209,325,20),None),
    ('pelvis',(209,338,20),(209,316,20),'root'),
    ('spine',(209,316,15),(209,282,15),'pelvis'),
    ('head',(209,282,12),(209,160,12),'spine'),
    ('upper_arm.L',(145,291,-15),(128,304,-35),'spine'),
    ('hand.L',(128,304,-48),(111,250,-58),'upper_arm.L'),
    ('upper_arm.R',(264,291,-15),(240,308,-35),'spine'),
    ('hand.R',(240,308,-48),(246,250,-58),'upper_arm.R'),
    ('leg.L',(162,331,15),(160,348,15),'pelvis'),
    ('foot.L',(160,348,12),(158,363,-5),'leg.L'),
    ('leg.R',(265,331,15),(265,348,15),'pelvis'),
    ('foot.R',(265,348,12),(265,363,-5),'leg.R'),
    ('ear.L',(69,168,-22),(49,172,-30),'head'),
    ('ear.R',(315,168,-22),(363,182,-30),'head'),
    ('tail',(280,309,30),(303,315,30),'pelvis'),
    ('eye.L',(131,201,-42),(131,188,-42),'head'),
    ('eye.R',(211,201,-42),(211,188,-42),'head'),
    ('nose',(164,219,-47),(164,211,-47),'head'),
    ('mouth',(165,244,-42),(165,235,-42),'head'),
]
for name,start,end,parent in bone_specs:
    bone=armature.edit_bones.new(name)
    bone.head=coord(*start)
    bone.tail=coord(*end)
    if parent:
        bone.parent=armature.edit_bones[parent]
bpy.ops.object.mode_set(mode='OBJECT')
rig.show_in_front=True
for ob in meshes:
    group=ob.vertex_groups.new(name=ob['rig_bone'])
    group.add(list(range(len(ob.data.vertices))),1,'REPLACE')
    modifier=ob.modifiers.new('Libby skeletal skin','ARMATURE')
    modifier.object=rig
    # Keep skinned nodes at scene root; the modifier supplies the skeleton.
    # This avoids glTF parent transforms being silently ignored by viewers.

scene=bpy.context.scene
scene.render.fps=30
scene.frame_start=1
scene.frame_end=82
# Bone movement follows the reference's small squash/rise and cheek press.
# Facial topology changes are intentionally not misrepresented as completed.
rig.animation_data_create()
clip=bpy.data.actions.new('crush_body_reference_study')
rig.animation_data.action=clip
for index in range(10):
    frame=1+index*9
    press=[0,.25,.5,.65,.8,1,.7,.4,.1,0][index]
    for name in ['root','head','hand.L','hand.R','ear.L','ear.R']:
        bone=rig.pose.bones[name]
        bone.rotation_mode='XYZ'
        bone.location=(0,0,0)
        bone.rotation_euler=(0,0,0)
        if name=='root':
            bone.location.y=press*.018
        elif name=='head':
            bone.rotation_euler.y=math.radians(-.5*press)
        elif name.startswith('hand.'):
            bone.rotation_euler.z=math.radians((1 if name.endswith('L') else -1)*2*press)
        elif name.startswith('ear.'):
            bone.rotation_euler.x=math.radians(1.5*press)
        bone.keyframe_insert('location',frame=frame)
        bone.keyframe_insert('rotation_euler',frame=frame)
    for target,values in expression_targets:
        target.value=values[index]
        target.keyframe_insert('value',frame=frame)
scene.frame_set(1)

scene.render.engine='BLENDER_WORKBENCH'
scene.display.shading.light='FLAT'
scene.display.shading.color_type='VERTEX'
scene.display.shading.show_shadows=False
scene.display.shading.show_cavity=False
scene.display.shading.show_specular_highlight=False
scene.display.shading.show_backface_culling=True
scene.display.render_aa='16'
scene.render.film_transparent=True
scene.view_settings.view_transform='Standard'
scene.render.resolution_x=836
scene.render.resolution_y=836
scene.render.resolution_percentage=100
scene.render.image_settings.file_format='PNG'
scene.render.image_settings.color_mode='RGBA'
bpy.ops.object.camera_add()
camera=bpy.context.object
camera.name='Original projection'
camera.data.type='ORTHO'
camera.data.ortho_scale=4.18
scene.camera=camera
target=Vector((0,.25,1.6))


def camera_at(angle):
    rad=math.radians(angle)
    camera.location=(math.sin(rad)*8,.25-math.cos(rad)*8,1.6)
    camera.rotation_euler=(target-camera.location).to_track_quat('-Z','Y').to_euler()


camera_at(0)
scene['asset_status']='voxel-likeness-and-rig-study-unapproved'
scene['scope']='crush reference study with bone motion and geometric facial morphs; full likeness and all actions not accepted'
scene['unverified']='Depth, sides, back and occluded joints are inferred. No 1:1 acceptance or runtime/performance acceptance.'
scene['pixel_grid_image_px']=CELL
scene['pixel_grid_depth_px']=DEPTH
bpy.ops.wm.save_as_mainfile(filepath=str(output/'libby-voxel.blend'),compress=True)
bpy.ops.object.select_all(action='DESELECT')
for ob in meshes+[rig]:
    ob.select_set(True)
bpy.context.view_layer.objects.active=rig
bpy.ops.export_scene.gltf(filepath=str(output/'libby-voxel.glb'),export_format='GLB',use_selection=True,export_animations=True,export_animation_mode='SCENE',export_anim_scene_split_object=False,export_skins=True,export_morph=True,export_materials='EXPORT',export_extras=True,export_vertex_color='MATERIAL',export_force_sampling=True,export_frame_range=True)
stats=[]
for ob in meshes:
    ob.data.calc_loop_triangles()
    bm=bmesh.new();bm.from_mesh(ob.data)
    stats.append({'name':ob.name,'vertices':len(ob.data.vertices),'triangles':len(ob.data.loop_triangles),'boundaryEdges':sum(e.is_boundary for e in bm.edges),'nonmanifoldEdges':sum(not e.is_manifold for e in bm.edges),'signedVolume':bm.calc_volume(signed=True),'morphTargets':len(ob.data.shape_keys.key_blocks)-1 if ob.data.shape_keys else 0})
    bm.free()
audit={'status':scene['asset_status'],'source':str(SOURCE.relative_to(repo)),'sourceSHA256':hashlib.sha256(SOURCE.read_bytes()).hexdigest(),'meshCount':len(meshes),'boneCount':len(armature.bones),'triangles':sum(s['triangles'] for s in stats),'parts':stats,'completedActions':[],'pending':'facial likeness refinement, all 15 accepted actions, native skinning, performance and platform acceptance'}
(output/'audit.json').write_text(json.dumps(audit,ensure_ascii=False,indent=2)+'\n')
if not args.no_render:
    for name,angle in [('front',0),('quarter',30),('side',90),('back',180)]:
        camera_at(angle)
        scene.render.filepath=str(output/(name+'.png'))
        bpy.ops.render.render(write_still=True)
    camera_at(30)
    scene.display.shading.light='STUDIO'
    scene.display.shading.show_cavity=True
    scene.display.shading.cavity_type='WORLD'
    scene.display.shading.curvature_ridge_factor=.5
    scene.display.shading.curvature_valley_factor=.5
    scene.render.filepath=str(output/'volume.png')
    bpy.ops.render.render(write_still=True)
    scene.display.shading.light='FLAT'
    scene.display.shading.show_cavity=False
    camera_at(0)
    for index,frame in enumerate(range(1,82,9),1):
        scene.frame_set(frame)
        scene.render.filepath=str(output/f'expression-{index:02}.png')
        bpy.ops.render.render(write_still=True)
    if args.animate_review:
        scene.render.resolution_x=418
        scene.render.resolution_y=418
        for frame in range(1,82,3):
            scene.frame_set(frame)
            camera_at(24)
            scene.render.filepath=str(output/'motion'/f'{frame:03}.png')
            bpy.ops.render.render(write_still=True)
print(json.dumps(audit,ensure_ascii=False),flush=True)
