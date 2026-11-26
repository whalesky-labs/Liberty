// Original-action voxel assets. This workflow does not activate production pets.
import fs from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {execFileSync,spawnSync} from 'node:child_process';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const work=path.join(root,'target/native/pet3d-fidelity');
const output=path.join(work,'actions');
const assets=path.join(root,'packages/desktop-resources/pet/models/candidates/pixel-actions');
const sources=path.join(root,'packages/desktop-resources/pet/models/source/pixel-actions');
const actions=['crush','defecate','drive','eat','gaming','pants','reading','rope','run','slack','sleep','snow','studying','toy','work'];
const generators=['scripts/blender/build-libby-actions.py','scripts/blender/libby_action_specs.py','scripts/blender/libby_voxel_mesh.py','scripts/blender/measure-libby-motion.py','scripts/blender/libby_pose_volumes.py','scripts/blender/libby_glb_pack.py'];
const sha=data=>createHash('sha256').update(data).digest('hex');
const json=file=>JSON.parse(fs.readFileSync(file,'utf8'));
const mode=process.argv[2]??'check';
function run(command,args){const result=spawnSync(command,args,{cwd:root,stdio:'inherit'});if(result.error)throw result.error;if(result.status!==0)throw new Error(`${path.basename(command)} failed (${result.signal??result.status})`);}
function inspect(data){
 if(data.length<28||data.readUInt32LE(0)!==0x46546c67||data.readUInt32LE(4)!==2||data.readUInt32LE(8)!==data.length)throw Error('Invalid GLB');
 const doc=JSON.parse(data.subarray(20,20+data.readUInt32LE(12)));
 if(doc.images?.length||doc.textures?.length)throw Error('Unexpected image texture');
 if(doc.skins?.length!==1||doc.skins[0].joints.length<16||doc.animations?.length!==1)throw Error('Missing rig or action');
 const primitives=doc.meshes.flatMap(m=>m.primitives);
 if(primitives.some(p=>p.attributes.JOINTS_0===undefined||p.attributes.WEIGHTS_0===undefined))throw Error('Unbound mesh');
 const channels=doc.animations[0].channels;
 if(!channels.some(c=>c.target.path==='weights')||!channels.some(c=>c.target.path==='translation'||c.target.path==='rotation'))throw Error('Missing skeletal or facial animation');
 const poseInputs=new Set();
 for(const channel of channels){
  const sampler=doc.animations[0].samplers[channel.sampler];
  if(sampler.interpolation!=='STEP')throw Error('Bones and corrective shapes must hold the same pixel pose');
  if(doc.accessors[sampler.input].count!==10)throw Error('Missing synchronized pose times');
  poseInputs.add(sampler.input);
 }
 if(poseInputs.size!==1)throw Error('Animation channels must share one pose timeline');
 const triangles=primitives.reduce((n,p)=>n+doc.accessors[p.indices].count/3,0);
 // The nine-pose anatomical corrections include closed props and hidden
 // surfaces. This is an authoring/review ceiling, not a runtime performance gate.
 if(triangles>150000||data.length>18*1024*1024)throw Error('Action exceeds review asset budget');
 return {triangles,bones:doc.skins[0].joints.length,meshes:doc.meshes.length,bytes:data.length,morphTargets:primitives.reduce((n,p)=>n+(p.targets?.length??0),0)};
}
async function validate(data,name){
 const module=path.join(root,'target/native/pet3d-pilot/validator/package/index.js');
 if(!fs.existsSync(module))throw Error('Khronos validator missing; install the pinned local validator before completing action checks');
 const result=await createRequire(import.meta.url)(module).validateBytes(new Uint8Array(data),{uri:name+'.glb',maxIssues:30});
 fs.mkdirSync(path.join(output,name),{recursive:true});fs.writeFileSync(path.join(output,name,'validator.json'),JSON.stringify(result,null,2)+'\n');
 if(result.issues.numErrors||result.issues.numWarnings)throw Error(name+': '+JSON.stringify(result.issues));
}
if(mode==='build'||mode==='collect'){
 const blender=process.env.LIBBY_BLENDER??path.join(work,'tools/Blender.app/Contents/MacOS/Blender');
 if(!execFileSync(blender,['--version'],{encoding:'utf8'}).startsWith('Blender 4.5.17 LTS'))throw Error('Blender 4.5.17 LTS required');
 if(mode==='build'){
  run(blender,['--background','--factory-startup','--python-exit-code','1','--python',generators[3],'--',root,path.join(work,'motion-fit')]);
  // Bound authoring memory: each action gets a fresh Blender process so its
  // orphan meshes, shape-key buffers and exporter cache cannot accumulate.
  for(const action of actions)run(blender,['--background','--factory-startup','--python-exit-code','1','--python',generators[0],'--',root,output,'--action',action]);
 }
 run(blender,['--background','--factory-startup','--python-exit-code','1','--python','scripts/blender/verify-libby-actions.py','--',output]);
 const manifest={version:1,status:'pixel-actions-review-required',style:'volumetric-pixel',completedActions:[],generatedActions:actions,blender:'4.5.17 LTS',generators:generators.map(file=>({file,sha256:sha(fs.readFileSync(path.join(root,file)))})),actions:[]};
 const prepared=[];
 for(const action of actions){
  const data=fs.readFileSync(path.join(output,action,action+'.glb')),audit=json(path.join(output,action,'audit.json'));
  if(audit.parts.some(p=>p.boundaryEdges||p.nonmanifoldEdges||p.signedVolume<=0))throw Error('Invalid geometry: '+action);
  const geometry=inspect(data);await validate(data,action);
  const blend=fs.readFileSync(path.join(output,action,action+'.blend'));
  prepared.push({action,data,blend});
  manifest.actions.push({id:action,title:audit.title,status:'visual-review-required',file:action+'.glb',sha256:sha(data),source:'../../source/pixel-actions/'+action+'.blend',sourceSHA256:sha(blend),references:audit.references,...geometry});
  console.log(action,geometry);
 }
 manifest.scope='All 15 original action scenes generated with closed components, actual skins and geometric facial morphs. Exact likeness, contacts and inferred sides require visual review.';
 run(process.execPath,['scripts/verify-libby-action-playback.mjs',output]);
 fs.mkdirSync(assets,{recursive:true});fs.mkdirSync(sources,{recursive:true});
 for(const {action,data,blend}of prepared){fs.writeFileSync(path.join(assets,action+'.glb'),data);fs.writeFileSync(path.join(sources,action+'.blend'),blend);}
 fs.writeFileSync(path.join(assets,'manifest.json'),JSON.stringify(manifest,null,2)+'\n');
}else if(mode==='check'){
 const manifest=json(path.join(assets,'manifest.json'));
 if(manifest.status!=='pixel-actions-review-required'||manifest.completedActions.length||manifest.actions.length!==15)throw Error('Incorrect acceptance/completeness status');
 for(const source of manifest.generators)if(sha(fs.readFileSync(path.join(root,source.file)))!==source.sha256)throw Error('Generator changed: '+source.file);
 for(const action of manifest.actions){
  const data=fs.readFileSync(path.join(assets,action.file));
  if(sha(data)!==action.sha256||sha(fs.readFileSync(path.join(assets,action.source)))!==action.sourceSHA256)throw Error('Asset hash: '+action.id);
  for(const ref of action.references)if(sha(fs.readFileSync(path.join(root,'packages/desktop-resources',ref.file)))!==ref.sha256)throw Error('Reference hash: '+ref.file);
  const info=inspect(data);for(const [key,value]of Object.entries(info))if(action[key]!==value)throw Error('Manifest mismatch: '+action.id+' '+key);
  await validate(data,action.id);console.log(action.id+': rig, hashes and Khronos validation passed');
 }
 run(process.execPath,['scripts/verify-libby-action-playback.mjs',assets]);
}else if(mode==='preview')run(process.execPath,['scripts/preview-libby-actions.mjs']);
else throw Error('Usage: node scripts/run-libby-actions.mjs build|collect|check|preview');
