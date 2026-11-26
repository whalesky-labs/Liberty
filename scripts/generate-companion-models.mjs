import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import {makePilot,encodeGLB,pilots,actions} from './lib/companion-model.mjs';
const root=fileURLToPath(new URL('../',import.meta.url)),shared=path.join(root,'packages/desktop-resources/pet/models'),embedded=path.join(root,'apps/native/internal/desktop/presentation/companion3d/assets');
const check=process.argv.includes('--check');
async function output(file,data){if(check){if(!Buffer.from(await readFile(file)).equals(Buffer.from(data)))throw new Error('Companion model drift: '+path.basename(file));}else{await mkdir(path.dirname(file),{recursive:true});await writeFile(file,data);}}
const manifest={version:1,status:'rejected',review:{date:'2026-11-25',reason:'Character proportions and likeness do not match the original artwork. Rendering fixtures only.'},completedActions:[],pilotActions:pilots,actions:[]};
for(const action of actions){
 const references=[];for(let i=1;i<=9;i++){const file=`images/pet/action/${action}/${action}_${String(i).padStart(2,'0')}.png`;const data=await readFile(path.join(root,'packages/desktop-resources',file));references.push({frame:i,file,sha256:createHash('sha256').update(data).digest('hex')});}
 const item={id:action,status:pilots.includes(action)?'rejected':'pending',references};
 if(pilots.includes(action)){const model=makePilot(action),data=encodeGLB(model);Object.assign(item,{file:action+'.glb',sha256:createHash('sha256').update(data).digest('hex'),triangles:model.meshes.reduce((n,g)=>n+g.idx.length/3,0),nodes:model.nodes.length});await output(path.join(shared,item.file),data);await output(path.join(embedded,item.file),data);}
 manifest.actions.push(item);
}
await output(path.join(shared,'manifest.json'),JSON.stringify(manifest,null,2)+'\n');
await output(path.join(embedded,'manifest.json'),JSON.stringify(manifest,null,2)+'\n');
console.log('3 rejected renderer fixtures retained; current pixel actions use their separate review manifest.');
