// Reproducible local asset workflow; no production asset activation.
import fs from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {execFileSync,spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {createRequire} from 'node:module';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const output=path.join(root,'target/native/pet3d-fidelity');
const built=path.join(output,'voxel');
const candidate=path.join(root,'packages/desktop-resources/pet/models/candidates/voxel-study');
const source=path.join(root,'packages/desktop-resources/pet/models/source/libby-voxel.blend');
const sha=bytes=>createHash('sha256').update(bytes).digest('hex');
const readJSON=file=>JSON.parse(fs.readFileSync(file,'utf8'));
const command=process.argv[2]??'check';

function run(executable,args){
  const result=spawnSync(executable,args,{cwd:root,stdio:'inherit'});
  if(result.error)throw result.error;
  if(result.status!==0)throw new Error(`${path.basename(executable)} exited ${result.status}`);
}

async function vendorViewer(){
  const base=path.join(output,'viewer/vendor');
  const files=['build/three.module.js','build/three.core.js','examples/jsm/loaders/GLTFLoader.js','examples/jsm/controls/OrbitControls.js','examples/jsm/utils/BufferGeometryUtils.js','LICENSE'];
  const integrity='o+qycAMZrh+TsE01GqWUxUIKR1AL0S8pq7zDkYOQw8GqfX8b8VoCKYUoHbhiX5j+7hr8XsuHDVU6+gkQJQKg9w==';
  const archive=path.join(output,'viewer/three-0.180.0.tgz');
  if(!fs.existsSync(archive)){
    const response=await fetch('https://registry.npmjs.org/three/-/three-0.180.0.tgz');
    if(!response.ok)throw new Error(`Three.js download failed: ${response.status}`);
    const bytes=Buffer.from(await response.arrayBuffer());
    if(createHash('sha512').update(bytes).digest('base64')!==integrity)throw new Error('Three.js integrity mismatch');
    fs.mkdirSync(path.dirname(archive),{recursive:true});fs.writeFileSync(archive,bytes);
  }
  if(createHash('sha512').update(fs.readFileSync(archive)).digest('base64')!==integrity)throw new Error('Cached Three.js integrity mismatch');
  for(const file of files){
    const destination=path.join(base,file);
    const bytes=execFileSync('tar',['-xOf',archive,'package/'+file],{maxBuffer:8<<20});
    fs.mkdirSync(path.dirname(destination),{recursive:true});fs.writeFileSync(destination,bytes);
  }
}

function inspect(data){
  if(data.readUInt32LE(0)!==0x46546c67||data.readUInt32LE(4)!==2||data.readUInt32LE(8)!==data.length)throw new Error('Invalid GLB');
  const doc=JSON.parse(data.subarray(20,20+data.readUInt32LE(12)));
  if(doc.images?.length||doc.textures?.length)throw new Error('Unexpected image textures in voxel candidate');
  if(doc.skins?.length!==1||doc.skins[0].joints.length<19)throw new Error('Missing character skin');
  if(doc.animations?.length!==1)throw new Error('Expected one crush review clip');
  const primitives=doc.meshes.flatMap(m=>m.primitives);
  if(primitives.some(p=>p.attributes.JOINTS_0===undefined||p.attributes.WEIGHTS_0===undefined))throw new Error('Mesh not bound to skeleton');
  const targets=primitives.reduce((n,p)=>n+(p.targets?.length??0),0);
  if(targets<10||!doc.animations[0].channels.some(c=>c.target.path==='weights'))throw new Error('Missing geometric facial animation');
  const triangles=primitives.reduce((n,p)=>n+doc.accessors[p.indices].count/3,0);
  if(triangles>50000||data.length>6*1024*1024)throw new Error('Review asset budget exceeded');
  return {triangles,bones:doc.skins[0].joints.length,skinnedMeshes:doc.meshes.length,morphTargets:targets,bytes:data.length};
}

if(command==='build'){
  const blender=process.env.LIBBY_BLENDER??path.join(output,'tools/Blender.app/Contents/MacOS/Blender');
  const version=execFileSync(blender,['--version'],{encoding:'utf8'});
  if(!version.startsWith('Blender 4.5.17 LTS'))throw new Error('This source requires Blender 4.5.17 LTS; set LIBBY_BLENDER to its executable');
  run(blender,['--background','--factory-startup','--python-exit-code','1','--python',path.join(root,'scripts/blender/build-libby-voxel.py'),'--',root,built,'--animate-review']);
  run(blender,['--background','--factory-startup','--python-exit-code','1','--python',path.join(root,'scripts/blender/verify-libby-voxel.py'),'--',path.join(built,'libby-voxel.blend')]);
  const bytes=fs.readFileSync(path.join(built,'libby-voxel.glb'));
  const geometry=inspect(bytes);
  const audit=readJSON(path.join(built,'audit.json'));
  if(audit.parts.some(p=>p.boundaryEdges||p.nonmanifoldEdges||p.signedVolume<=0))throw new Error('Open or nonmanifold mesh; do not publish the candidate');
  fs.mkdirSync(candidate,{recursive:true});
  fs.copyFileSync(path.join(built,'libby-voxel.glb'),path.join(candidate,'libby-voxel.glb'));
  fs.copyFileSync(path.join(built,'libby-voxel.blend'),source);
  const references=Array.from({length:9},(_,i)=>{const file=`images/pet/action/crush/crush_${String(i+1).padStart(2,'0')}.png`;return {frame:i+1,file,sha256:sha(fs.readFileSync(path.join(root,'packages/desktop-resources',file)))};});
  const manifest={version:1,status:'voxel-study-unapproved',style:'volumetric-pixel',action:'crush',completedActions:[],source:'../../source/libby-voxel.blend',sourceSHA256:sha(fs.readFileSync(source)),file:'libby-voxel.glb',sha256:sha(bytes),generator:'scripts/blender/build-libby-voxel.py',generatorSHA256:sha(fs.readFileSync(path.join(root,'scripts/blender/build-libby-voxel.py'))),blender:'4.5.17 LTS',...geometry,references,scope:'Bone and geometric facial animation study; not accepted as 1:1; sides and rear inferred.',pending:['likeness acceptance','remaining 14 actions','native skin/morph rendering','CPU and memory acceptance','macOS Intel and Windows verification']};
  fs.writeFileSync(path.join(candidate,'manifest.json'),JSON.stringify(manifest,null,2)+'\n');
  console.log('Saved unapproved voxel candidate:',geometry);
}else if(command==='check'){
  const manifest=readJSON(path.join(candidate,'manifest.json'));
  if(manifest.status!=='voxel-study-unapproved'||manifest.completedActions.length)throw new Error('Candidate incorrectly marked accepted');
  const bytes=fs.readFileSync(path.join(candidate,manifest.file));
  if(sha(bytes)!==manifest.sha256||sha(fs.readFileSync(source))!==manifest.sourceSHA256)throw new Error('Model/source checksum mismatch');
  if(sha(fs.readFileSync(path.join(root,manifest.generator)))!==manifest.generatorSHA256)throw new Error('Rebuild candidate after generator changes');
  for(const reference of manifest.references)if(sha(fs.readFileSync(path.join(root,'packages/desktop-resources',reference.file)))!==reference.sha256)throw new Error('Reference changed: '+reference.file);
  const geometry=inspect(bytes);
  for(const [key,value]of Object.entries(geometry))if(manifest[key]!==value)throw new Error('Manifest geometry mismatch: '+key);
  const validator=path.join(root,'target/native/pet3d-pilot/validator/package/index.js');
  if(fs.existsSync(validator)){
    const {validateBytes}=createRequire(import.meta.url)(validator);
    const result=await validateBytes(new Uint8Array(bytes),{uri:'libby-voxel.glb',maxIssues:100});
    fs.mkdirSync(built,{recursive:true});fs.writeFileSync(path.join(built,'validator.json'),JSON.stringify(result,null,2)+'\n');
    if(result.issues.numErrors||result.issues.numWarnings)throw new Error(JSON.stringify(result.issues));
    console.log('Khronos validation: zero errors / zero warnings');
  }else console.log('Khronos validator unavailable; only repository structural checks executed.');
  console.log('Voxel candidate checks passed:',geometry);
}else if(command==='preview'){
  await vendorViewer();
  fs.mkdirSync(built,{recursive:true});
  fs.copyFileSync(path.join(candidate,'libby-voxel.glb'),path.join(built,'libby-voxel.glb'));
  fs.copyFileSync(source,path.join(built,'libby-voxel.blend'));
  run(process.execPath,[path.join(root,'scripts/preview-libby-voxel.mjs')]);
}else throw new Error('Usage: node scripts/run-libby-voxel.mjs build|check|preview');
