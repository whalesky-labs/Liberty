"""Rounded Libby character, authored from the original crush proportions.

Blender 4.5.17 LTS. The original images constrain landmarks and colour; no
image is projected on the character. Smooth closed surfaces, an armature and
geometric facial morphs are exported. Unseen anatomy remains inferred.
"""
import argparse
import hashlib
import json
import math
from pathlib import Path
import sys

import bpy
import bmesh
import numpy as np
from mathutils import Vector

parser=argparse.ArgumentParser()
parser.add_argument('repo',type=Path)
parser.add_argument('output',type=Path)
parser.add_argument('--animate-review',action='store_true')
parser.add_argument('--no-render',action='store_true')
args=parser.parse_args(sys.argv[sys.argv.index('--')+1:])
repo,output=args.repo.resolve(),args.output.resolve()
output.mkdir(parents=True,exist_ok=True)
bpy.ops.object.select_all(action='SELECT')
bpy.ops.object.delete(use_global=False)
S=.01


def coord(x,y,depth):
    return Vector(((x-209)*S,depth*S,(369-y)*S))


def linear(color):
    values=np.asarray(color,dtype=float)/255
    return tuple(np.where(values<=.04045,values/12.92,((values+.055)/1.055)**2.4))+(1,)


palette={
    'wool':([249,247,239],.83),
    'skin':([255,205,165],.67),
    'horn':([143,83,44],.52),
    'horn_groove':([105,59,33],.64),
    'hoof':([128,70,37],.61),
    'eye':([40,26,16],.16),
    'iris':([88,51,25],.24),
    'nose':([88,42,24],.30),
    'mouth':([78,32,23],.55),
    'tongue':([236,118,103],.45),
    'heart':([239,105,98],.32),
    'star':([255,211,74],.37),
    'white':([255,254,247],.18),
    'crease':([192,131,94],.75),
    'brow':([112,70,38],.72),
}
materials={}
for name,(color,roughness) in palette.items():
    material=bpy.data.materials.new('Libby '+name)
    material.diffuse_color=linear(color)
    material.use_nodes=True
    shader=material.node_tree.nodes.get('Principled BSDF')
    shader.inputs['Base Color'].default_value=linear(color)
    shader.inputs['Roughness'].default_value=roughness
    shader.inputs['Metallic'].default_value=0
    if name=='skin':
        shader.inputs['Subsurface Weight'].default_value=.025
        shader.inputs['Subsurface Radius'].default_value=(1,.45,.25)
    materials[name]=material

meshes=[]
morphs=[]


def resample(points,count):
    lengths=[math.dist(p,points[(i+1)%len(points)]) for i,p in enumerate(points)]
    length=sum(lengths)
    edge=0
    offset=0
    result=[]
    for i in range(count):
        distance=i/count*length
        while edge<len(points)-1 and offset+lengths[edge]<distance:
            offset+=lengths[edge];edge+=1
        t=(distance-offset)/max(.00001,lengths[edge])
        a,b=points[edge],points[(edge+1)%len(points)]
        result.append((a[0]*(1-t)+b[0]*t,a[1]*(1-t)+b[1]*t))
    return result


def rounded(points,count=64,iterations=3):
    # Corner cutting removes the source's pixel steps before surface creation.
    for _ in range(iterations):
        output=[]
        for i,a in enumerate(points):
            b=points[(i+1)%len(points)]
            output.extend([(a[0]*.75+b[0]*.25,a[1]*.75+b[1]*.25),(a[0]*.25+b[0]*.75,a[1]*.25+b[1]*.75)])
        points=output
    return resample(points,count)


def ellipse(cx,cy,rx,ry,count=64,power=1):
    def signpow(x):return math.copysign(abs(x)**power,x)
    return [(cx+rx*signpow(math.cos(i/count*math.tau)),cy+ry*signpow(math.sin(i/count*math.tau))) for i in range(count)]


def mesh_object(name,vertices,faces,material,bone):
    data=bpy.data.meshes.new(name)
    data.from_pydata(vertices,[],faces)
    data.update()
    bm=bmesh.new();bm.from_mesh(data)
    bmesh.ops.recalc_face_normals(bm,faces=list(bm.faces))
    bm.to_mesh(data);bm.free();data.update()
    for poly in data.polygons:poly.use_smooth=True
    ob=bpy.data.objects.new(name,data)
    bpy.context.collection.objects.link(ob)
    ob.data.materials.append(materials[material])
    ob['rig_bone']=bone
    ob['anatomy']=name
    meshes.append(ob)
    return ob


def solid(name,outline,center,edge,bulge,rear,material,bone,count=64,rings=10,paint=None):
    """Smooth contour volume: continuous front and rear, closed at both poles.

    The contour has the character's measured proportions. Rings follow a
    spherical parameterization rather than stacks of extruded pixel slices.
    """
    contour=rounded(outline,count)
    cx,cy=center
    vertices=[coord(cx,cy,edge-bulge)]
    for j in range(1,rings*2):
        angle=j/(rings*2)*math.pi
        r=math.sin(angle)
        d=edge-(bulge if j<=rings else rear)*math.cos(angle)
        vertices.extend(coord(cx+(x-cx)*r,cy+(y-cy)*r,d) for x,y in contour)
    rear_index=len(vertices)
    vertices.append(coord(cx,cy,edge+rear))
    faces=[]
    for i in range(count):faces.append((0,1+(i+1)%count,1+i))
    for j in range(rings*2-2):
        for i in range(count):
            a=1+j*count+i;b=1+j*count+(i+1)%count
            faces.append((a,b,b+count,a+count))
    base=1+(rings*2-2)*count
    for i in range(count):faces.append((base+i,base+(i+1)%count,rear_index))
    ob=mesh_object(name,vertices,faces,material,bone)
    # Refine the authored contour surface before binding. This removes radial
    # normal pinching without replacing the head or hands with primitive balls.
    bpy.ops.object.select_all(action='DESELECT');ob.select_set(True);bpy.context.view_layer.objects.active=ob
    refinement=ob.modifiers.new('Rounded surface refinement','SUBSURF')
    refinement.levels=1;refinement.render_levels=1
    bpy.ops.object.modifier_apply(modifier=refinement.name)
    # Keep the exported model economical; quad topology remains editable.
    reduction=ob.modifiers.new('Preserve curved silhouette','DECIMATE')
    reduction.ratio=.24
    bpy.ops.object.modifier_apply(modifier=reduction.name)
    for poly in ob.data.polygons:poly.use_smooth=True
    if paint:
        attr=ob.data.color_attributes.new(name='Color',type='FLOAT_COLOR',domain='POINT')
        for v,item in zip(ob.data.vertices,attr.data):item.color=paint(v.co)
        mat=materials[material].copy();mat.name=name+' surface colour'
        node=mat.node_tree.nodes.new('ShaderNodeVertexColor');node.layer_name='Color'
        shader=mat.node_tree.nodes.get('Principled BSDF')
        shader.inputs['Base Color'].default_value=(1,1,1,1)
        mat.node_tree.links.new(node.outputs['Color'],shader.inputs['Base Color'])
        ob.data.materials.clear();ob.data.materials.append(mat)
    return ob


def tube(name,points,radius,material,bone,radii=None,resolution=3):
    curve=bpy.data.curves.new(name,'CURVE');curve.dimensions='3D';curve.resolution_u=6
    spline=curve.splines.new('BEZIER');spline.bezier_points.add(len(points)-1)
    for i,(point,xyz) in enumerate(zip(spline.bezier_points,points)):
        point.co=coord(*xyz);point.handle_left_type='AUTO';point.handle_right_type='AUTO'
        if radii:point.radius=radii[i]
    curve.bevel_depth=radius*S;curve.bevel_resolution=resolution;curve.use_fill_caps=True
    ob=bpy.data.objects.new(name,curve);bpy.context.collection.objects.link(ob)
    bpy.ops.object.select_all(action='DESELECT');ob.select_set(True);bpy.context.view_layer.objects.active=ob
    bpy.ops.object.convert(target='MESH');ob=bpy.context.object
    bm=bmesh.new();bm.from_mesh(ob.data)
    bmesh.ops.remove_doubles(bm,verts=list(bm.verts),dist=.000001)
    bmesh.ops.recalc_face_normals(bm,faces=list(bm.faces))
    bm.to_mesh(ob.data);bm.free();ob.data.update()
    for p in ob.data.polygons:p.use_smooth=True
    ob.data.materials.append(materials[material]);ob['rig_bone']=bone;ob['anatomy']=name;meshes.append(ob)
    return ob


hood_outline=[(73,243),(72,211),(73,175),(80,145),(94,120),(114,101),(138,86),(156,83),(174,67),(185,55),(198,55),(201,42),(217,43),(225,55),(238,59),(247,76),(275,81),(284,98),(304,117),(324,135),(338,159),(343,188),(341,226),(335,248),(320,263),(298,273),(274,280),(247,281),(215,283),(175,281),(141,275),(110,267),(87,257)]
# The hood is one continuous warm-white form. Its rear uses the same material.
hood=solid('hood',hood_outline,(208,171),5,40,106,'wool','head',count=80,rings=12)

face_outline=[(96,244),(96,216),(101,201),(102,177),(114,162),(139,153),(149,154),(158,160),(199,161),(210,152),(219,159),(240,166),(253,181),(258,196),(266,203),(264,229),(257,245),(244,261),(224,276),(192,279),(166,277),(142,271),(120,260),(104,253)]
face_center=(181,215)
face_contour=rounded(face_outline,96)


def boundary_radius(x,y,points=face_contour,center=face_center):
    dx,dy=x-center[0],y-center[1]
    length=math.hypot(dx,dy)
    if length<1e-7:return 0
    dx/=length;dy/=length
    crosses=[]
    for i,a in enumerate(points):
        b=points[(i+1)%len(points)];sx,sy=b[0]-a[0],b[1]-a[1]
        den=dx*sy-dy*sx
        if abs(den)<1e-7:continue
        ax,ay=a[0]-center[0],a[1]-center[1]
        r=(ax*sy-ay*sx)/den;t=(ax*dy-ay*dx)/den
        if r>0 and 0<=t<=1:crosses.append(r)
    return min(1,length/min(crosses)) if crosses else 1


def face_surface(x,y):
    r=boundary_radius(x,y)
    return -27-30*math.sqrt(max(0,1-r*r))


hood_contour=rounded(hood_outline,80)
for name,points in [
    ('wool_tuft_upper',[(185,68),(193,74),(207,76)]),
    ('wool_tuft_right',[(229,80),(238,86),(251,85)]),
    ('wool_tuft_left',[(154,88),(161,96),(174,98)]),
]:
    projected=[]
    for x,y in points:
        r=boundary_radius(x,y,hood_contour,(208,171))
        projected.append((x,y,5-40*math.sqrt(max(0,1-r*r))-1.8))
    tube(name,projected,2.4,'wool','head',radii=[.25,1,.3])


def face_paint(co):
    x,y,d=co.x/S+209,369-co.z/S,co.y/S
    color=np.array([255.,207.,168.])
    if d< -26:
        for cx in [111,220]:
            blush=.50*math.exp(-(((x-cx)/23)**2+((y-231)/16)**2)*1.8)
            color=color*(1-blush)+np.array([239.,145.,125.])*blush
            for i in range(3):
                bx=cx-12+i*9
                t=max(0,min(1,((x-bx)*8+(239-y)*13)/233))
                distance=(x-(bx+8*t))**2+(y-(239-13*t))**2
                stroke=.48*math.exp(-distance/5.5)
                color=color*(1-stroke)+np.array([219.,111.,91.])*stroke
    return linear(color)


face=solid('face',face_outline,face_center,-27,30,35,'skin','head',count=96,rings=16,paint=face_paint)
# A thin, rounded wool seam surrounds the face opening. It conforms to the
# cheek shape and avoids the previous floating dark outline around the face.
for name,points in [('forehead_wool',[(108,174,-34),(122,157,-33),(143,153,-32),(160,158,-34),(198,158,-34),(212,153,-32),(238,166,-33),(253,182,-33)])]:
    tube(name,points,3.0,'wool','head',radii=[.35,.75,1,.6,.6,1,.75,.35])

body_outline=[(140,276),(173,279),(219,278),(249,282),(269,290),(280,307),(282,322),(271,338),(248,347),(181,350),(158,342),(142,333),(132,316),(131,299)]
solid('body',body_outline,(209,313),15,43,51,'wool','spine',count=64,rings=10)
solid('tail',ellipse(278,311,15,17), (278,311),48,15,20,'wool','tail',count=32,rings=8)

horns={
 'L':([(69,75),(97,74),(102,83),(117,87),(120,96),(113,108),(103,113),(100,124),(91,122),(87,110),(77,103),(75,93),(68,90)],(95,96)),
 'R':([(281,97),(298,88),(310,88),(321,80),(334,80),(338,74),(360,74),(365,82),(359,94),(346,101),(338,111),(328,115),(327,129),(309,129),(298,121),(284,116)],(321,101)),
}
for side,(outline,center) in horns.items():
    solid('horn.'+side,outline,center,4,15,27,'horn','head',count=40,rings=8)
tube('horn_groove.L',[(104,83,-6),(103,94,-10),(98,105,-9),(95,117,-2)],1.2,'horn_groove','head',radii=[.4,1,1,.3])
tube('horn_groove.R1',[(309,90,-8),(317,99,-12),(323,112,-11),(323,125,-2)],1.4,'horn_groove','head',radii=[.4,1,1,.3])
tube('horn_groove.R2',[(336,79,-5),(338,90,-10),(349,98,-4)],1.3,'horn_groove','head',radii=[.3,1,.3])

ears={
 'L':([(48,165),(59,162),(67,155),(73,161),(73,178),(65,183),(53,181),(46,175)],(61,171)),
 'R':([(310,159),(327,159),(339,168),(349,173),(368,178),(372,184),(364,192),(343,193),(330,185),(317,182),(308,173)],(338,176)),
}
for side,(outline,center) in ears.items():
    solid('ear.'+side,outline,center,-22,15,20,'skin','ear.'+side,count=40,rings=8)

hands={
 'L':([(93,248),(100,242),(108,244),(115,251),(125,257),(133,265),(141,277),(144,289),(140,306),(130,313),(116,313),(108,307),(107,295),(110,282),(103,277),(96,268),(92,257)],(122,279)),
 'R':([(208,274),(216,265),(229,257),(236,247),(247,244),(260,248),(265,258),(263,269),(253,278),(241,283),(241,290),(251,292),(266,287),(276,292),(280,301),(274,310),(257,316),(239,317),(222,314),(211,307),(206,293)],(239,282)),
}
for side,(outline,center) in hands.items():
    solid('hand.'+side,outline,center,-57,14,28,'skin','hand.'+side,count=64,rings=10)
    shoulder=(145,295,-11) if side=='L' else (265,294,-10)
    elbow=(123,302,-45) if side=='L' else (247,305,-45)
    tube('upper_arm.'+side,[shoulder,((shoulder[0]+elbow[0])/2,302,-27),elbow],10,'skin','upper_arm.'+side,radii=[.85,1,.95],resolution=4)
tube('fingers.L',[(101,247,-64),(102,254,-68),(106,260,-68)],1.1,'crease','hand.L',radii=[.3,1,.4])
tube('fingers.R1',[(239,250,-65),(239,256,-68),(237,261,-70)],1.1,'crease','hand.R',radii=[.3,1,.4])
tube('fingers.R2',[(251,250,-65),(252,257,-68),(249,263,-70)],1.1,'crease','hand.R',radii=[.3,1,.4])
tube('thumb.R',[(241,281,-69),(238,286,-71),(241,292,-69)],1.0,'crease','hand.R',radii=[.4,1,.4])

for side,cx in [('L',157),('R',265)]:
    solid('leg.'+side,ellipse(cx,342,18,14),(cx,342),15,18,20,'skin','leg.'+side,count=40,rings=7)
    solid('foot.'+side,ellipse(cx,356,21,9,power=.70),(cx,356),7,24,23,'hoof','foot.'+side,count=40,rings=7)


def conform_solid(name,outline,center,front,thickness,material,bone,count=40,rings=7):
    ob=solid(name,outline,center,0,front,thickness,material,bone,count=count,rings=rings)
    for v in ob.data.vertices:
        x,y=v.co.x/S+209,369-v.co.z/S
        v.co.y+=face_surface(x,y)*S
    ob.data.update()
    return ob


def expanded_morph(ob,weights,center=None,retreat=.12):
    full=[v.co.copy() for v in ob.data.vertices]
    if center is None:center=sum(full,Vector())/len(full)
    center=center+Vector((0,retreat,0))
    for v in ob.data.vertices:v.co=center+(v.co-center)*.01
    ob.shape_key_add(name='Basis')
    target=ob.shape_key_add(name='Expand')
    for v,co in zip(target.data,full):v.co=co
    morphs.append((target,weights))
    return target


heart_points=[]
for i in range(64):
    t=i/64*math.tau
    heart_points.append((16*math.sin(t)**3,-(13*math.cos(t)-5*math.cos(2*t)-2*math.cos(3*t)-math.cos(4*t))-.5))

for side,cx in [('L',131),('R',211)]:
    eye=conform_solid('eye.'+side,ellipse(cx,201,12.7,14.7),(cx,201),4.5,2,'eye','eye.'+side,count=48,rings=9)
    eye.shape_key_add(name='Basis');love=eye.shape_key_add(name='Love')
    # Preserve the regular closed eye volume while changing its outline to a
    # heart; the red fill is an independent geometric expression component.
    for v,target in zip(eye.data.vertices,love.data):
        px,py=v.co.x/S+209-cx,369-v.co.z/S-201
        angle=math.atan2(py,px);dx,dy=math.cos(angle),math.sin(angle)
        radii=[]
        for j,a in enumerate(heart_points):
            b=heart_points[(j+1)%len(heart_points)];sx,sy=b[0]-a[0],b[1]-a[1]
            den=dx*sy-dy*sx
            if abs(den)<1e-6:continue
            r=(a[0]*sy-a[1]*sx)/den;t=(a[0]*dy-a[1]*dx)/den
            if r>0 and 0<=t<=1:radii.append(r)
        r=min(radii) if radii else 14
        amount=math.sqrt((px/12.7)**2+(py/14.7)**2)
        target.co.x=(cx+dx*r*amount-209)*S
        target.co.z=(369-201-dy*r*amount)*S
    morphs.append((love,[0,0,0,0,1,1,1,0,0,0]))
    iris=conform_solid('iris.'+side,ellipse(cx+1,207,7,7),(cx+1,207),1,1,'iris','eye.'+side,count=32,rings=6)
    for v in iris.data.vertices:v.co.y-=.038
    heart=conform_solid('heart.'+side,[(cx+x*.82,201+y*.82) for x,y in heart_points],(cx,201),2,1,'heart','eye.'+side,count=48,rings=8)
    for v in heart.data.vertices:v.co.y-=.06
    expanded_morph(heart,[0,0,0,0,1,1,1,0,0,0])
    star_points=[]
    for i in range(8):
        a=i/8*math.tau-math.pi/2;r=11 if i%2==0 else 4
        star_points.append((cx+r*math.cos(a),201+r*math.sin(a)))
    star=conform_solid('star.'+side,star_points,(cx,201),1,1,'star','eye.'+side,count=32,rings=6)
    for v in star.data.vertices:v.co.y-=.065
    expanded_morph(star,[0,0,0,1,0,0,0,0,0,0])
    for name,dx,dy,rx,ry in [('glint',-4,-6,3.2,4),('glint_small',4,7,1.4,1.5)]:
        glint=conform_solid(name+'.'+side,ellipse(cx+dx,201+dy,rx,ry),(cx+dx,201+dy),1,1,'white','eye.'+side,count=24,rings=5)
        for v in glint.data.vertices:v.co.y-=.085
    for i,(dx,dy) in enumerate([(-7,8),(6,-1),(-1,12)]):
        sparkle=conform_solid(f'sparkle.{side}.{i}',ellipse(cx+dx,201+dy,2,2),(cx+dx,201+dy),1,1,'white','eye.'+side,count=20,rings=5)
        for v in sparkle.data.vertices:v.co.y-=.085
        expanded_morph(sparkle,[0,1,1,0,0,0,0,1,0,0])
    tube('eyebrow.'+side,[(cx-8,173,face_surface(cx-8,173)-1),(cx,171,face_surface(cx,171)-1),(cx+8,172,face_surface(cx+8,172)-1)],1.7,'brow','head',radii=[.6,1,.5])

conform_solid('nose',ellipse(164,218,13,7.7,power=.65),(164,218),7,3,'nose','nose',count=40,rings=8)

mouth_outline=rounded([(150,240),(153,244),(173,244),(179,239),(177,246),(170,247),(155,247)],48)
mouth=conform_solid('mouth_cavity',mouth_outline,(164,243),1.4,1.2,'mouth','mouth',count=48,rings=7)
mouth.shape_key_add(name='Basis');open_mouth=mouth.shape_key_add(name='Open')
for v,target in zip(mouth.data.vertices,open_mouth.data):
    x,y=v.co.x/S+209,369-v.co.z/S
    target_y=236+(y-239)*2.8
    target.co.z=(369-target_y)*S
    target.co.y+=(face_surface(x,target_y)-face_surface(x,y)-1)*S
morphs.append((open_mouth,[0,0,.65,0,.8,1,.7,0,0,0]))
tongue=conform_solid('tongue',ellipse(164,252,9,4.8),(164,252),1,1,'tongue','mouth',count=32,rings=7)
for v in tongue.data.vertices:v.co.y-=.045
expanded_morph(tongue,[0,0,.65,0,.8,1,.7,0,0,0],coord(164,244,face_surface(164,244)-3),retreat=0)
drool=conform_solid('drool',ellipse(179,252,1.8,6),(179,252),1,1,'white','mouth',count=24,rings=6)
for v in drool.data.vertices:v.co.y-=.02
expanded_morph(drool,[0,0,0,1,0,0,0,0,0,0])

# One armature, all renderable parts bound. Continuous arm/hand meshes have
# blended elbow weights, while eyes, horns and facial details move rigidly.
armature=bpy.data.armatures.new('Libby cartoon skeleton')
rig=bpy.data.objects.new('LibbyRig',armature);bpy.context.collection.objects.link(rig)
bpy.ops.object.select_all(action='DESELECT');rig.select_set(True);bpy.context.view_layer.objects.active=rig
bpy.ops.object.mode_set(mode='EDIT')
specs=[
 ('root',(209,352,20),(209,325,20),None),('pelvis',(209,338,20),(209,316,20),'root'),
 ('spine',(209,316,15),(209,282,15),'pelvis'),('head',(209,282,12),(209,160,12),'spine'),
 ('upper_arm.L',(145,291,-15),(128,304,-35),'spine'),('hand.L',(128,304,-48),(111,250,-58),'upper_arm.L'),
 ('upper_arm.R',(264,291,-15),(240,308,-35),'spine'),('hand.R',(240,308,-48),(246,250,-58),'upper_arm.R'),
 ('leg.L',(162,331,15),(160,348,15),'pelvis'),('foot.L',(160,348,12),(158,363,-5),'leg.L'),
 ('leg.R',(265,331,15),(265,348,15),'pelvis'),('foot.R',(265,348,12),(265,363,-5),'leg.R'),
 ('ear.L',(69,168,-22),(49,172,-30),'head'),('ear.R',(315,168,-22),(363,182,-30),'head'),
 ('tail',(280,309,30),(303,315,30),'pelvis'),('eye.L',(131,201,-42),(131,188,-42),'head'),
 ('eye.R',(211,201,-42),(211,188,-42),'head'),('nose',(164,219,-47),(164,211,-47),'head'),
 ('mouth',(165,244,-42),(165,235,-42),'head'),
]
for name,start,end,parent in specs:
    b=armature.edit_bones.new(name);b.head=coord(*start);b.tail=coord(*end)
    if parent:b.parent=armature.edit_bones[parent]
bpy.ops.object.mode_set(mode='OBJECT');rig.show_in_front=True
for ob in meshes:
    bone=ob['rig_bone'];group=ob.vertex_groups.new(name=bone)
    group.add(list(range(len(ob.data.vertices))),1,'REPLACE')
    if ob.name in ['hand.L','hand.R']:
        parent=ob.vertex_groups.new(name='upper_arm.'+ob.name[-1])
        for v in ob.data.vertices:
            y=369-v.co.z/S
            weight=max(0,min(.3,(y-295)/50))
            if weight:
                group.add([v.index],1-weight,'REPLACE');parent.add([v.index],weight,'REPLACE')
    mod=ob.modifiers.new('Libby skeletal skin','ARMATURE');mod.object=rig

scene=bpy.context.scene;scene.render.fps=30;scene.frame_start=1;scene.frame_end=82
rig.animation_data_create();rig.animation_data.action=bpy.data.actions.new('crush_cartoon_reference_study')
for index in range(10):
    frame=1+index*9;press=[0,.25,.5,.65,.8,1,.7,.4,.1,0][index]
    for name in ['root','head','hand.L','hand.R','ear.L','ear.R']:
        bone=rig.pose.bones[name];bone.rotation_mode='XYZ';bone.location=(0,0,0);bone.rotation_euler=(0,0,0)
        if name=='root':bone.location.y=press*.018
        elif name=='head':bone.rotation_euler.y=math.radians(-.5*press)
        elif name.startswith('hand.'):bone.rotation_euler.z=math.radians((1 if name.endswith('L') else -1)*2*press)
        else:bone.rotation_euler.x=math.radians(1.5*press)
        bone.keyframe_insert('location',frame=frame);bone.keyframe_insert('rotation_euler',frame=frame)
    for target,weights in morphs:target.value=weights[index];target.keyframe_insert('value',frame=frame)
scene.frame_set(1)

scene['asset_status']='cartoon-likeness-and-rig-study-unapproved'
scene['scope']='Rounded cartoon proportions from crush references; smooth surfaces and shared front/rear materials'
scene['unverified']='Side/back anatomy inferred. Full likeness, remaining actions, native performance and platform acceptance pending.'
scene.render.engine='BLENDER_EEVEE_NEXT'
scene.render.film_transparent=True
scene.view_settings.view_transform='Standard'
scene.render.resolution_x=836;scene.render.resolution_y=836;scene.render.resolution_percentage=100
scene.render.image_settings.file_format='PNG';scene.render.image_settings.color_mode='RGBA'
world=bpy.data.worlds.new('Neutral studio');scene.world=world;world.use_nodes=True
world.node_tree.nodes.get('Background').inputs[0].default_value=(.82,.85,.9,1)
world.node_tree.nodes.get('Background').inputs[1].default_value=.25
target=Vector((0,.30,1.6))


def light(name,location,energy,size):
    data=bpy.data.lights.new(name,'AREA');data.energy=energy;data.shape='DISK';data.size=size
    ob=bpy.data.objects.new(name,data);bpy.context.collection.objects.link(ob);ob.location=location
    ob.rotation_euler=(target-ob.location).to_track_quat('-Z','Y').to_euler()


light('Soft key',(-3.5,-4.5,6),220,4)
light('Soft fill',(4,-3,3.3),120,4)
light('Rear fill',(0,4.5,4.5),210,4)
light('Top glow',(-1,1,6),90,3)
bpy.ops.object.camera_add();camera=bpy.context.object;camera.name='Original projection'
camera.data.type='ORTHO';camera.data.ortho_scale=4.18;scene.camera=camera


def camera_at(degrees):
    a=math.radians(degrees);camera.location=(math.sin(a)*8,.30-math.cos(a)*8,1.6)
    camera.rotation_euler=(target-camera.location).to_track_quat('-Z','Y').to_euler()


camera_at(0)
bpy.ops.wm.save_as_mainfile(filepath=str(output/'libby-cartoon.blend'),compress=True)
bpy.ops.object.select_all(action='DESELECT')
for ob in meshes+[rig]:ob.select_set(True)
bpy.context.view_layer.objects.active=rig
bpy.ops.export_scene.gltf(filepath=str(output/'libby-cartoon.glb'),export_format='GLB',use_selection=True,export_animations=True,export_animation_mode='SCENE',export_anim_scene_split_object=False,export_skins=True,export_morph=True,export_materials='EXPORT',export_extras=True,export_vertex_color='MATERIAL',export_force_sampling=True,export_frame_range=True)
parts=[]
for ob in meshes:
    ob.data.calc_loop_triangles();bm=bmesh.new();bm.from_mesh(ob.data)
    parts.append({'name':ob.name,'vertices':len(ob.data.vertices),'triangles':len(ob.data.loop_triangles),'boundaryEdges':sum(e.is_boundary for e in bm.edges),'nonmanifoldEdges':sum(not e.is_manifold for e in bm.edges),'signedVolume':bm.calc_volume(signed=True),'morphTargets':len(ob.data.shape_keys.key_blocks)-1 if ob.data.shape_keys else 0})
    bm.free()
audit={'status':scene['asset_status'],'meshCount':len(meshes),'boneCount':len(armature.bones),'triangles':sum(p['triangles'] for p in parts),'parts':parts,'completedActions':[],'pending':'likeness acceptance, other 14 actions, native skin/morph rendering and performance/platform acceptance'}
(output/'audit.json').write_text(json.dumps(audit,indent=2)+'\n')
if not args.no_render:
    for name,angle in [('front',0),('quarter',30),('side',90),('back',180)]:
        camera_at(angle);scene.render.filepath=str(output/(name+'.png'));bpy.ops.render.render(write_still=True)
    for i,frame in enumerate([1,10,19,28,37,46,55,64,73],1):
        camera_at(0);scene.frame_set(frame);scene.render.filepath=str(output/f'expression-{i:02}.png');bpy.ops.render.render(write_still=True)
    if args.animate_review:
        scene.render.resolution_x=418;scene.render.resolution_y=418
        for frame in range(1,82,3):
            scene.frame_set(frame);camera_at(24);scene.render.filepath=str(output/'motion'/f'{frame:03}.png');bpy.ops.render.render(write_still=True)
print(json.dumps({'status':audit['status'],'meshes':len(meshes),'bones':len(armature.bones),'triangles':audit['triangles'],'geometryIssues':[p for p in parts if p['boundaryEdges'] or p['nonmanifoldEdges'] or p['signedVolume']<=0]}),flush=True)
