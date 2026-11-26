"""Verify the saved rig's actual evaluated geometry, not only GLB metadata."""
import bpy
import bmesh
import json
import math
from pathlib import Path
import sys

model=Path(sys.argv[sys.argv.index('--')+1]).resolve()
bpy.ops.wm.open_mainfile(filepath=str(model))
scene=bpy.context.scene
rig=bpy.data.objects['LibbyRig']
meshes=[o for o in scene.objects if o.type=='MESH']
assert len(rig.data.bones)==19
assert scene['asset_status']=='cartoon-likeness-and-rig-study-unapproved'
assert bpy.data.objects['hood'].data.materials[0] == bpy.data.objects['body'].data.materials[0] == bpy.data.objects['tail'].data.materials[0], 'Wool front/rear material mismatch'
for ob in meshes:
    assert len(ob.modifiers)==1 and ob.modifiers[0].type=='ARMATURE'
    assert ob.modifiers[0].object==rig
    assert all(p.use_smooth for p in ob.data.polygons),'Faceted surface: '+ob.name
    for v in ob.data.vertices:
        assert v.groups and abs(sum(g.weight for g in v.groups)-1)<1e-6
        assert all(math.isfinite(x) for x in v.co)
    bm=bmesh.new();bm.from_mesh(ob.data)
    assert all(e.is_manifold for e in bm.edges),ob.name
    assert bm.calc_volume(signed=True)>0,ob.name
    bm.free()

def snapshot(frame):
    scene.frame_set(frame)
    graph=bpy.context.evaluated_depsgraph_get()
    result={}
    for ob in meshes:
        evaluated=ob.evaluated_get(graph)
        mesh=evaluated.to_mesh()
        points=[tuple(evaluated.matrix_world @ v.co) for v in mesh.vertices]
        assert all(math.isfinite(value) for p in points for value in p)
        result[ob.name]=points
        evaluated.to_mesh_clear()
    return result

start=snapshot(1)
middle=snapshot(46)
end=snapshot(82)
max_loop_error=max(math.dist(a,b) for name in start for a,b in zip(start[name],end[name]))
assert max_loop_error<1e-5,('loop discontinuity',max_loop_error)
motion={name:max(math.dist(a,b) for a,b in zip(start[name],middle[name])) for name in start}
assert motion['hand.L']>.005 and motion['hand.R']>.005,'Bones did not move the hands'
assert motion['eye.L']>.02 and motion['mouth_cavity']>.02,'Facial morphs did not deform the geometry'
assert all(not node.image for material in bpy.data.materials if material.use_nodes for node in material.node_tree.nodes if node.type=='TEX_IMAGE')
print(json.dumps({'result':'passed','meshes':len(meshes),'bones':len(rig.data.bones),'maxLoopError':max_loop_error,'leftHandMotion':motion['hand.L'],'eyeMorphMotion':motion['eye.L'],'mouthMorphMotion':motion['mouth_cavity']},indent=2))
