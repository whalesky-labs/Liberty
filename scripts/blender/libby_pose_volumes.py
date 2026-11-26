"""Closed, articulated pixel volumes with source-constrained key poses.

Each anatomical component owns one mesh throughout the action. Equal-colour,
equal-depth cells are combined into rectangular voxel prisms before building
its nine shape targets. The rig supplies continuous motion; the corrective
targets hold the authored pixel contours between reference poses.
"""
import math

import bpy
import bmesh
import numpy as np

import libby_voxel_mesh as vox

coverage = None


def rectangles(cells):
    """Partition a labelled grid without changing its silhouette or colours."""
    remaining = cells.copy()
    height, width = remaining.shape
    for row in range(height):
        col = 0
        while col < width:
            value = remaining[row, col]
            if value < 0:
                col += 1
                continue
            right = col + 1
            while right < width and remaining[row, right] == value:
                right += 1
            best_right, bottom, area = right, row + 1, right - col
            probe = row + 1
            # Select the largest available rectangle anchored at this cell.
            # A maximal-width row alone produces many one-row slivers around
            # curved depth bands and antialiased colour boundaries.
            while probe < height and remaining[probe, col] == value:
                stop = col + 1
                while stop < right and remaining[probe, stop] == value:
                    stop += 1
                right = min(right, stop)
                candidate = (right - col) * (probe - row + 1)
                if candidate > area:
                    best_right, bottom, area = right, probe + 1, candidate
                probe += 1
            right = best_right
            remaining[row:bottom, col:right] = -1
            yield int(value), (col * 2, row * 2, right * 2, bottom * 2)
            col = right


def build(name, masks, visible, colours, palette, fronts, backs, base,
          transforms, bone, morphs):
    """Make nine corrections on a fixed collection of closed voxel prisms.

The partition is per semantic component, never per character frame. Hidden
surfaces use the component material instead of mirrored source artwork.
All target coordinates are expressed in the inverse pose of their actual bone.
"""
    if coverage is not None:
        masks = [mask & coverage[index] for index,mask in enumerate(masks)]
    frames = []
    edge_distances = []
    palette_float = palette.astype(float)
    base_id = int(((palette_float - base) ** 2).sum(axis=1).argmin())
    for index, mask in enumerate(masks):
        edge = np.zeros(mask.shape, dtype=int)
        layer = mask.copy()
        for _ in range(4):
            edge[layer] += 1
            padded = np.pad(layer, 1)
            layer = padded[1:-1,1:-1]&padded[:-2,1:-1]&padded[2:,1:-1]&padded[1:-1,:-2]&padded[1:-1,2:]
        edge_distances.append(edge)
        front = np.floor(np.broadcast_to(fronts[index], mask.shape) / 8) * 8
        rear_step = 16 if name in ('hood','body') else 8
        back = np.maximum(front + 8, np.ceil(np.broadcast_to(backs[index], mask.shape) / rear_step) * rear_step)
        rows, cols = np.where(mask)
        groups = {}
        if len(rows):
            rgb = colours[index, rows, cols].astype(float)
            colour_ids = ((rgb[:, None] - palette_float[None]) ** 2).sum(axis=2).argmin(axis=1)
            colour_ids[~visible[index][rows, cols]] = base_id
            # A compact grid code includes both depth levels. It lets the
            # greedy partition merge thousands of coplanar same-colour cells.
            values = np.stack([colour_ids, front[rows, cols], back[rows, cols]], axis=1).astype(int)
            unique, inverse = np.unique(values, axis=0, return_inverse=True)
            cells = np.full(mask.shape, -1, dtype=int)
            cells[rows, cols] = inverse
            for value, rect in rectangles(cells):
                colour, near, far = unique[value]
                groups.setdefault(int(colour), []).append((*rect, float(near), float(far)))
        frames.append(groups)
    if not any(frames):
        return None

    all_rects = [rect for groups in frames for rects in groups.values() for rect in rects]
    cx = float(np.mean([(v[0] + v[2]) / 2 for v in all_rects]))
    cy = float(np.mean([(v[1] + v[3]) / 2 for v in all_rects]))
    vertices, faces, facecolours = [], [], []
    targets = [[] for _ in range(9)]
    inverses = [np.linalg.inv(matrix) for matrix in transforms]
    depth_scales = [math.sqrt(abs(np.linalg.det(matrix[:2, :2]))) for matrix in transforms]
    for colour in range(len(palette)):
        sets = [sorted(groups.get(colour, []), key=lambda r: (r[1] // 12, r[0] // 12, r[1], r[0])) for groups in frames]
        for slot in range(max(map(len, sets))):
            rear_colour = base
            if name in ('hood','body','tail'):
                for pose,rects in enumerate(sets):
                    if slot >= len(rects):continue
                    left,top,right,bottom,_,_=rects[slot]
                    row,col=int((top+bottom)/4),int((left+right)/4)
                    edge=edge_distances[pose][min(row,208),min(col,208)]
                    if edge<=1:rear_colour=[43,39,37]
                    elif edge<=3:rear_colour=[197,199,193]
                    break
            for index, rects in enumerate(sets):
                if slot < len(rects):
                    left, top, right, bottom, near, far = rects[slot]
                else:
                    # Dormant cells are subpixel solids inside the component,
                    # so the topology remains closed in every target.
                    left, top, right, bottom, near, far = cx, cy, cx + .001, cy + .001, 8, 8.001
                points = [(left, top), (right, top), (right, bottom), (left, bottom)]
                for depth in (near, far):
                    for x, y in points:
                        local = inverses[index] @ np.array([x, y, 1])
                        targets[index].append(vox.coord(float(local[0]), float(local[1]), depth / depth_scales[index]))
            start = len(vertices)
            vertices.extend(targets[0][-8:])
            # Correct outward winding in (image x, depth, -image y) coordinates.
            for side, corners in enumerate([(0, 1, 2, 3), (7, 6, 5, 4), (4, 5, 1, 0), (5, 6, 2, 1), (6, 7, 3, 2), (7, 4, 0, 3)]):
                faces.append(tuple(start + i for i in corners))
                facecolours.append(palette[colour] if side == 0 else rear_colour if side == 1 else base)

    mesh = bpy.data.meshes.new(name)
    mesh.from_pydata(vertices, [], faces)
    mesh.update()
    bm = bmesh.new()
    bm.from_mesh(mesh)
    bmesh.ops.recalc_face_normals(bm, faces=list(bm.faces))
    bm.to_mesh(mesh)
    bm.free()
    attr = mesh.color_attributes.new(name='Color', type='FLOAT_COLOR', domain='CORNER')
    lookup = {tuple(map(int, colour)): vox.srgb_linear(colour) for colour in facecolours}
    rgba = np.array([lookup[tuple(map(int, colour))] for colour in facecolours], dtype=np.float32)
    attr.data.foreach_set('color', np.repeat(rgba, 4, axis=0).ravel())
    obj = bpy.data.objects.new(name, mesh)
    bpy.context.collection.objects.link(obj)
    mesh.materials.append(vox.material)
    obj['rig_bone'] = bone
    obj['anatomy'] = name
    obj['source'] = vox.source_name
    obj['depth_authority'] = 'inferred-volume; nine source-visible contours and colours'
    obj.shape_key_add(name='Basis')
    for index in range(1, 9):
        key = obj.shape_key_add(name=f'PoseCorrection_{index + 1}')
        key.data.foreach_set('co', np.asarray(targets[index], dtype=np.float32).ravel())
        morphs.append((key, index))
    vox.meshes.append(obj)
    print('Pose volume', name, len(vertices), len(faces) * 2, flush=True)
    return obj
