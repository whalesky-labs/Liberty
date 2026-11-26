"""Pack Blender's GLB without changing pixel poses or skinning semantics.

Drop stored normals (glTF requires flat normals when absent), weld vertices only
when ALL eight morph trajectories and colours agree, quantize skin weights and
vertex colours to normalized bytes, and deduplicate identical accessors.
No new runtime extension or decoder is required.
"""
import json
import struct

import numpy as np


def pack(file):
    original=file.read_bytes()
    length=struct.unpack_from('<I',original,12)[0]
    doc=json.loads(original[20:20+length])
    binary=original[28+length:]
    old_accessors=doc['accessors'];old_views=doc['bufferViews']
    components={5120:'i1',5121:'u1',5122:'<i2',5123:'<u2',5125:'<u4',5126:'<f4'}
    sizes={'SCALAR':1,'VEC2':2,'VEC3':3,'VEC4':4,'MAT4':16}
    def read(index):
        accessor=old_accessors[index]
        dtype=np.dtype(components[accessor['componentType']]);width=sizes[accessor['type']]
        if 'bufferView' in accessor:
            view=old_views[accessor['bufferView']]
            offset=view.get('byteOffset',0)+accessor.get('byteOffset',0)
            stride=view.get('byteStride',width*dtype.itemsize)
            array=np.ndarray((accessor['count'],width),dtype=dtype,buffer=binary,offset=offset,strides=(stride,dtype.itemsize)).copy()
        else:array=np.zeros((accessor['count'],width),dtype=dtype)
        if 'sparse' in accessor:
            sparse=accessor['sparse'];idx=sparse['indices'];values=sparse['values']
            offset=old_views[idx['bufferView']].get('byteOffset',0)+idx.get('byteOffset',0)
            indices=np.frombuffer(binary,dtype=components[idx['componentType']],count=sparse['count'],offset=offset)
            offset=old_views[values['bufferView']].get('byteOffset',0)+values.get('byteOffset',0)
            array[indices]=np.frombuffer(binary,dtype=dtype,count=sparse['count']*width,offset=offset).reshape(-1,width)
        return array
    accessors=[];views=[];payload=bytearray();cache={}
    def write(array,typ,component,normalized=False,target=None,bounds=False):
        array=np.ascontiguousarray(array,dtype=components[component])
        data=array.tobytes()
        key=(data,typ,component,normalized,target,bounds)
        if key in cache:return cache[key]
        payload.extend(b'\0'*((-len(payload))%4))
        view={'buffer':0,'byteOffset':len(payload),'byteLength':len(data)}
        if target:view['target']=target
        view_index=len(views);views.append(view);payload.extend(data)
        accessor={'bufferView':view_index,'componentType':component,'count':len(array),'type':typ}
        if normalized:accessor['normalized']=True
        if bounds:accessor.update(min=array.min(axis=0).tolist(),max=array.max(axis=0).tolist())
        index=len(accessors);accessors.append(accessor);cache[key]=index
        return index
    for mesh in doc['meshes']:
        for primitive in mesh['primitives']:
            attributes={name:read(index) for name,index in primitive['attributes'].items() if name not in ('NORMAL','TANGENT')}
            if 'COLOR_0' in attributes:
                if attributes['COLOR_0'].dtype.kind=='f':
                    attributes['COLOR_0']=np.rint(attributes['COLOR_0']*255).clip(0,255).astype('u1')
                else:assert attributes['COLOR_0'].dtype==np.dtype('u1')
                if attributes['COLOR_0'].shape[1]==3:
                    attributes['COLOR_0']=np.column_stack([attributes['COLOR_0'],np.full(len(attributes['COLOR_0']),255,dtype='u1')])
            if 'WEIGHTS_0' in attributes:
                # The authoring rig binds every component rigidly to one bone.
                if attributes['WEIGHTS_0'].dtype.kind=='f':
                    assert np.all((attributes['WEIGHTS_0']==0)|(attributes['WEIGHTS_0']==1))
                    attributes['WEIGHTS_0']=(attributes['WEIGHTS_0']*255).astype('u1')
                else:assert np.all((attributes['WEIGHTS_0']==0)|(attributes['WEIGHTS_0']==255))
            targets=[{name:read(index) for name,index in target.items() if name=='POSITION'} for target in primitive.get('targets',[])]
            arrays=[*attributes.values(),*[a for target in targets for a in target.values()]]
            joined=np.concatenate([array.view('u1').reshape(len(array),-1) for array in arrays],axis=1)
            records=np.ascontiguousarray(joined).view(np.dtype((np.void,joined.shape[1]))).ravel()
            _,indices,inverse=np.unique(records,return_index=True,return_inverse=True)
            attrs={}
            for name,array in attributes.items():
                old=old_accessors[primitive['attributes'][name]]
                normalized=name in ('COLOR_0','WEIGHTS_0')
                component=5121 if normalized else old['componentType']
                attrs[name]=write(array[indices],'VEC4' if name=='COLOR_0' else old['type'],component,normalized,34962,name=='POSITION')
            primitive['attributes']=attrs
            old_indices=read(primitive['indices']).ravel()
            new_indices=inverse[old_indices].reshape(-1,1)
            primitive['indices']=write(new_indices,'SCALAR',5123 if len(indices)<65536 else 5125,False,34963)
            primitive['targets']=[{name:write(array[indices],'VEC3',5126,False,34962,True) for name,array in target.items()} for target in targets]
    def copy(index):
        old=old_accessors[index]
        return write(read(index),old['type'],old['componentType'],old.get('normalized',False),None,'min' in old)
    for skin in doc.get('skins',[]):skin['inverseBindMatrices']=copy(skin['inverseBindMatrices'])
    for animation in doc.get('animations',[]):
        weight_samplers={channel['sampler']:channel for channel in animation['channels'] if channel['target']['path']=='weights'}
        # Blender exports scene frame 1 at 1 / fps, whereas the source viewer
        # starts its first pose at zero. Normalize the entire bone timeline,
        # otherwise shapes and bones disagree at every reference boundary.
        origin=min(float(read(sampler['input'])[0,0]) for index,sampler in enumerate(animation['samplers']) if index not in weight_samplers)
        pose_times=(np.arange(10,dtype=np.float32)*.3).reshape(-1,1)
        for index,sampler in enumerate(animation['samplers']):
            if index in weight_samplers:
                node=doc['nodes'][weight_samplers[index]['target']['node']]
                count=len(doc['meshes'][node['mesh']]['primitives'][0]['targets'])
                assert count==8
                weights=np.zeros((10,8),dtype=np.float32)
                for pose in range(1,9):weights[pose,pose-1]=1
                sampler['input']=write(pose_times,'SCALAR',5126,bounds=True)
                sampler['output']=write(weights.reshape(-1,1),'SCALAR',5126)
            else:
                source_times=(np.rint((read(sampler['input'])-origin)*30)/30).ravel()
                source_values=read(sampler['output'])
                assert sampler.get('interpolation','LINEAR')!='CUBICSPLINE'
                # Source key values are retained exactly. Every component and
                # morph now uses the same ten timestamps, including the loop.
                selected=[]
                for time in pose_times.ravel():
                    nearest=int(np.abs(source_times-time).argmin())
                    if abs(source_times[nearest]-time)>1e-5:
                        # Blender may collapse an unchanging channel to its
                        # endpoints. Expansion is safe only for constant data.
                        assert np.max(np.abs(source_values-source_values[0]))<1e-6
                        nearest=0
                    selected.append(source_values[nearest])
                sampler['input']=write(pose_times,'SCALAR',5126,bounds=True)
                sampler['output']=write(np.asarray(selected),old_accessors[sampler['output']]['type'],5126)
            sampler['interpolation']='STEP'
    doc['accessors']=accessors;doc['bufferViews']=views
    doc['buffers']=[{'byteLength':len(payload)}]
    encoded=json.dumps(doc,separators=(',',':')).encode();encoded+=b' '*((-len(encoded))%4)
    payload.extend(b'\0'*((-len(payload))%4))
    packed=struct.pack('<III',0x46546c67,2,28+len(encoded)+len(payload))+struct.pack('<II',len(encoded),0x4e4f534a)+encoded+struct.pack('<II',len(payload),0x004e4942)+payload
    file.write_bytes(packed)
    return {'beforeBytes':len(original),'afterBytes':len(packed)}
