"""Evaluate saved action rigs, all nine poses and the loop seam in Blender."""
import bpy
import bmesh
import json
import math
from pathlib import Path
import sys

root=Path(sys.argv[sys.argv.index('--')+1]).resolve()
files=[root] if root.suffix=='.blend' else sorted(root.glob('*/*.blend'))
results=[]
for file in files:
    bpy.ops.wm.open_mainfile(filepath=str(file))
    scene=bpy.context.scene;rig=bpy.data.objects['LibbyRig']
    assert scene['asset_status']=='pixel-action-review-required'
    assert scene.get('pose_timing')=='synchronized-pixel-poses'
    meshes=[o for o in scene.objects if o.type=='MESH']
    for ob in meshes:
        assert len(ob.modifiers)==1 and ob.modifiers[0].type=='ARMATURE' and ob.modifiers[0].object==rig,ob.name
        assert all(v.groups and abs(sum(g.weight for g in v.groups)-1)<1e-6 for v in ob.data.vertices),ob.name
        bm=bmesh.new();bm.from_mesh(ob.data)
        assert all(e.is_manifold for e in bm.edges),('nonmanifold',ob.name)
        assert bm.calc_volume(signed=True)>0,('invalid volume',ob.name)
        bm.free()
    def snapshot(frame):
        scene.frame_set(frame);graph=bpy.context.evaluated_depsgraph_get();out={}
        for ob in meshes:
            evaluated=ob.evaluated_get(graph);mesh=evaluated.to_mesh()
            points=[tuple(evaluated.matrix_world@v.co) for v in mesh.vertices]
            assert all(math.isfinite(v) for p in points for v in p),ob.name
            out[ob.name]=points;evaluated.to_mesh_clear()
        return out
    first=snapshot(1);max_motion=0
    pose_bounds=[];max_assembly_drift=0
    for frame in range(1,74,9):
        points=snapshot(frame)
        pose_bounds.append({name:{'min':[min(p[axis] for p in values) for axis in range(3)],'max':[max(p[axis] for p in values) for axis in range(3)]} for name,values in points.items()})
        for offset in (1,4,8):
            held=snapshot(frame+offset)
            drift=max(math.dist(a,b) for name in points for a,b in zip(points[name],held[name]))
            max_assembly_drift=max(max_assembly_drift,drift)
            assert drift<1e-6,('assembly separates between key poses',file.name,frame+offset,drift)
    for frame in sorted(set(range(10,74,9))|set(range(5,82,9))):
        current=snapshot(frame)
        for ob in meshes:
            if ob.data.shape_keys:
                assert all(abs(key.value-round(key.value))<1e-6 for key in ob.data.shape_keys.key_blocks[1:]),('interpolated pixel shape',ob.name,frame)
        max_motion=max(max_motion,max(math.dist(a,b) for name in first for a,b in zip(first[name],current[name])))
    final=snapshot(82)
    seam=max(math.dist(a,b) for name in first for a,b in zip(first[name],final[name]))
    assert seam<1e-5,('loop discontinuity',file.name,seam)
    assert max_motion>.005,('no evaluated motion',file.name)
    assert not any(node.type=='TEX_IMAGE' for mat in bpy.data.materials if mat.use_nodes for node in mat.node_tree.nodes)
    result={'action':scene['action'],'meshes':len(meshes),'bones':len(rig.data.bones),'maxLoopError':seam,'maxMotion':max_motion,'maxAssemblyDrift':max_assembly_drift,'result':'passed'}
    print(json.dumps(result),flush=True);result['poseBounds']=pose_bounds;results.append(result)
if root.is_dir():(root/'rig-validation.json').write_text(json.dumps(results,indent=2)+'\n')
