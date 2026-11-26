// Independent CPU pose samples from the same pinned loader used for visual review.
import fs from 'node:fs';
import path from 'node:path';
import {registerHooks} from 'node:module';
import {fileURLToPath,pathToFileURL} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const vendor=path.join(root,'target/native/pet3d-fidelity/viewer/vendor');
registerHooks({resolve(specifier,context,next){if(specifier==='three')return {url:pathToFileURL(path.join(vendor,'build/three.module.js')).href,shortCircuit:true};return next(specifier,context);}});
const THREE=await import(pathToFileURL(path.join(vendor,'build/three.module.js')).href);
const {GLTFLoader}=await import(pathToFileURL(path.join(vendor,'examples/jsm/loaders/GLTFLoader.js')).href);
const folder=path.join(root,'packages/desktop-resources/pet/models/candidates/pixel-actions');
const manifest=JSON.parse(fs.readFileSync(path.join(folder,'manifest.json'),'utf8'));
const reference={version:1,source:'three@0.180.0 GLTFLoader, getVertexPosition and world matrices',actions:[]};
for(const asset of manifest.actions){
 const bytes=fs.readFileSync(path.join(folder,asset.file));
 const gltf=await new GLTFLoader().parseAsync(bytes.buffer.slice(bytes.byteOffset,bytes.byteOffset+bytes.byteLength),'');
 const mixer=new THREE.AnimationMixer(gltf.scene);mixer.clipAction(gltf.animations[0]).play();
 const samples=[];gltf.scene.traverse(mesh=>{if(!mesh.isSkinnedMesh)return;const count=mesh.geometry.attributes.position.count;const indices=[0,Math.floor(count/5),Math.floor(count/2),Math.floor(count*4/5),count-1];samples.push({mesh,part:gltf.parser.json.meshes[gltf.parser.associations.get(mesh).meshes].name,indices,poses:[]});});
 for(let frame=0;frame<9;frame++){
  mixer.setTime(frame*.3+1e-6);gltf.scene.updateMatrixWorld(true);
  for(const sample of samples){sample.mesh.skeleton.update();sample.poses.push(sample.indices.map(i=>{const v=new THREE.Vector3();sample.mesh.getVertexPosition(i,v);v.applyMatrix4(sample.mesh.matrixWorld);return v.toArray().map(x=>Math.round(x*1e7)/1e7);}));}
 }
 reference.actions.push({id:asset.id,sha256:asset.sha256,samples:samples.map(({mesh,...sample})=>sample)});
 mixer.stopAllAction();mixer.uncacheRoot(gltf.scene);const skeletons=new Set();for(const {mesh} of samples){mesh.geometry.dispose();mesh.material.dispose();skeletons.add(mesh.skeleton);}for(const s of skeletons)s.dispose();
}
const output=path.join(folder,'native-reference.json'),data=JSON.stringify(reference)+'\n';
if(process.argv.includes('--check')){if(fs.readFileSync(output,'utf8')!==data)throw Error('Native pose reference drift');}else fs.writeFileSync(output,data);
console.log('15 native pose reference sets match the pinned Three.js loader.');
