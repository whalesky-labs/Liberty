// Exercise the actual pinned Three.js loader, skinning and animation mixer.
// CPU pose checks complement the browser visual review; no GPU result is implied.
import fs from 'node:fs';
import path from 'node:path';
import {registerHooks} from 'node:module';
import {fileURLToPath,pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const vendor=path.join(root,'target/native/pet3d-fidelity/viewer/vendor');
registerHooks({resolve(specifier,context,next){
 if(specifier==='three')return {url:pathToFileURL(path.join(vendor,'build/three.module.js')).href,shortCircuit:true};
 return next(specifier,context);
}});
const THREE=await import(pathToFileURL(path.join(vendor,'build/three.module.js')).href);
const {GLTFLoader}=await import(pathToFileURL(path.join(vendor,'examples/jsm/loaders/GLTFLoader.js')).href);
const folder=process.argv[2]?path.resolve(process.argv[2]):path.join(root,'packages/desktop-resources/pet/models/candidates/pixel-actions');
const files=fs.readdirSync(folder,{withFileTypes:true}).flatMap(entry=>{
 const file=entry.isDirectory()?path.join(entry.name,entry.name+'.glb'):entry.name;
 return file.endsWith('.glb')&&fs.existsSync(path.join(folder,file))?[file]:[];
});
assert.equal(files.length,15);
const results=[];
const evaluated=JSON.parse(fs.readFileSync(path.join(root,'target/native/pet3d-fidelity/actions/rig-validation.json'),'utf8'));
for(const file of files){
 const bytes=fs.readFileSync(path.join(folder,file));
 const gltf=await new GLTFLoader().parseAsync(bytes.buffer.slice(bytes.byteOffset,bytes.byteOffset+bytes.byteLength),'');
 const meshes=[];gltf.scene.traverse(o=>{if(o.isSkinnedMesh)meshes.push(o);});
 assert.ok(meshes.length>=14,file);
 const mixer=new THREE.AnimationMixer(gltf.scene),action=mixer.clipAction(gltf.animations[0]);
 action.setLoop(THREE.LoopOnce,1);action.clampWhenFinished=true;action.play();
 const v=new THREE.Vector3();
 function snapshot(time){
  mixer.setTime(time);gltf.scene.updateMatrixWorld(true);
  return meshes.flatMap(mesh=>{
   mesh.skeleton.update();
   assert.ok(mesh.morphTargetInfluences.every(w=>Math.abs(w-Math.round(w))<1e-5),file+' '+mesh.name+' '+time);
   const points=[];const count=mesh.geometry.attributes.position.count;
   // Sample every anatomical part, including its trailing vertices; keep the
   // check bounded even for unusually detailed props.
   for(let i=0;i<count;i+=Math.max(1,Math.floor(count/300))){
    mesh.getVertexPosition(i,v);v.applyMatrix4(mesh.matrixWorld);
    assert.ok(v.toArray().every(Number.isFinite),file);
    assert.ok(v.length()<12,file+' out-of-bounds evaluated pose');
    points.push(v.x,v.y,v.z);
   }
   return points;
  });
 }
 const first=snapshot(0);let motion=0;
 // Contact regression: an authored pixel pose is an indivisible assembly.
 // Moving bones while its corrective shape is held separates the neck, arms,
 // props and rope handles even though each key pose and loop seam is valid.
 // Check both sides of every pose boundary and every displayed 30 fps sample.
 let maxAssemblyDrift=0;
 for(let frame=0;frame<9;frame++){
  const referencePose=snapshot(frame*.3+1e-6);
  for(const offset of [1/30,2/30,3/30,4/30,5/30,6/30,7/30,8/30,.3-1e-5]){
   const currentPose=snapshot(frame*.3+offset);
   currentPose.forEach((value,index)=>{maxAssemblyDrift=Math.max(maxAssemblyDrift,Math.abs(value-referencePose[index]));});
  }
 }
 assert.ok(maxAssemblyDrift<1e-5,file+' splits its authored pose between keys: '+maxAssemblyDrift);
 const reference=evaluated.find(result=>result.action===path.basename(file,'.glb'));
 assert.ok(reference?.poseBounds,'Missing evaluated Blender pose evidence: '+file);
 let maxExportError=0,worstExport;
 for(let frame=0;frame<9;frame++){
  snapshot(frame*.3+1e-6);
  for(const mesh of meshes){
   const min=[Infinity,Infinity,Infinity],max=[-Infinity,-Infinity,-Infinity];
   for(let index=0;index<mesh.geometry.attributes.position.count;index++){
    mesh.getVertexPosition(index,v);v.applyMatrix4(mesh.matrixWorld);
    v.toArray().forEach((n,axis)=>{min[axis]=Math.min(min[axis],n);max[axis]=Math.max(max[axis],n);});
   }
   const sourceName=gltf.parser.json.meshes[gltf.parser.associations.get(mesh).meshes].name;
   const expected=reference.poseBounds[frame][sourceName];assert.ok(expected,file+' '+sourceName);
   const sourceMin=[expected.min[0],expected.min[2],-expected.max[1]],sourceMax=[expected.max[0],expected.max[2],-expected.min[1]];
   min.forEach((value,index)=>{const error=Math.max(Math.abs(value-sourceMin[index]),Math.abs(max[index]-sourceMax[index]));if(error>maxExportError){maxExportError=error;worstExport={frame,sourceName,index,min,max,sourceMin,sourceMax};}});
   mesh.morphTargetInfluences.forEach((weight,index)=>assert.ok(Math.abs(weight-Number(frame===index+1))<1e-5,file+' delayed reference pose '+frame));
  }
 }
 assert.ok(maxExportError<.0001,file+' differs from authored Blender poses: '+maxExportError+' '+JSON.stringify(worstExport));
 for(const t of [.15,.3,.45,.6,.75,.9,1.05,1.2,1.35,1.5,1.65,1.8,1.95,2.1,2.25,2.4,2.55]){
  const pose=snapshot(t);pose.forEach((v,i)=>{motion=Math.max(motion,Math.abs(v-first[i]));});
 }
 const last=snapshot(gltf.animations[0].duration);let seam=0;last.forEach((v,i)=>{seam=Math.max(seam,Math.abs(v-first[i]));});
 assert.ok(motion>.005,file+' missing evaluated animation');assert.ok(seam<.0001,file+' loop seam '+seam);
 results.push({action:path.basename(file,'.glb'),meshes:meshes.length,maxLoopError:seam,maxMotion:motion,maxExportError,maxAssemblyDrift,result:'passed'});
 mixer.stopAllAction();mixer.uncacheRoot(gltf.scene);
 const skeletons=new Set();for(const mesh of meshes){mesh.geometry.dispose();mesh.material.dispose();skeletons.add(mesh.skeleton);}
 for(const skeleton of skeletons)skeleton.dispose();
 console.log(file+': Three.js skinning, synchronized contacts, key/intermediate poses and loop passed');
}
fs.writeFileSync(path.join(root,'target/native/pet3d-fidelity/actions/playback-validation.json'),JSON.stringify(results,null,2)+'\n');
