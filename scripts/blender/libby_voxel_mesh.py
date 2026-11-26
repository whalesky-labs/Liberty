"""Shared closed voxel meshing for original-art component reconstruction.

Configured by the action builder. No frame cards or image textures are emitted.
"""
import bpy
import bmesh
import numpy as np

S=.01
CELL=2
DEPTH=8
SHAPE=(209,209)
meshes=[]
stats=[]
rgb=None
material=None
source_name=''

def coord(x,y,depth):return ((x-209)*S,depth*S,(369-y)*S)

def srgb_linear(color):
    v=np.asarray(color,dtype=float)/255
    return tuple(np.where(v<=.04045,v/12.92,((v+.055)/1.055)**2.4))+(1,)

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
        visible = mask
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
    # Rear surface colours extend the same material, including contour ink.
    # Never copy the face/eyes onto the back or add arbitrary checker patches.
    edge_distance=np.zeros(SHAPE,dtype=np.int16)
    layer=mask.copy()
    for _ in range(12):
        edge_distance[layer]+=1
        pad=np.pad(layer,1)
        layer=pad[1:-1,1:-1]&pad[:-2,1:-1]&pad[2:,1:-1]&pad[1:-1,:-2]&pad[1:-1,2:]
    wool=name in ('hood','body','tail')
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
                if wool:
                    color=np.array([246,246,241])
                    if normal==(0,0,1) and edge_distance[y,x]<=1:color=np.array([43,39,37])
                    elif normal==(0,0,1) and edge_distance[y,x]<=3:color=np.array([191,193,190])
                    elif normal==(1,0,0):color=np.array([218,220,215])
                elif edge_distance[y,x]<=1 and normal==(0,0,1):
                    color=np.array([48,36,25])
                elif normal==(1,0,0):
                    color=np.maximum(0,color-16)
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
    ob['source'] = source_name
    ob['depth_authority'] = 'inferred-volume; source fixes visible contour/colour'
    ob['rig_bone'] = bone or name
    meshes.append(ob)
    stats.append({'name': name, 'vertices': len(mesh.vertices), 'triangles': len(mesh.loop_triangles), 'depthPixels': int((max_d - min_d)*DEPTH)})
    print('Solid', name, len(mesh.vertices), len(mesh.loop_triangles), flush=True)
    return ob

