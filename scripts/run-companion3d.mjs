import {spawn} from 'node:child_process';
import {copyFile,mkdir,readFile,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('../',import.meta.url)),cwd=path.join(root,'apps/native');
const env={...process.env,GOTOOLCHAIN:'go1.27.4',GOFLAGS:'-mod=readonly'};
function run(command,args,extra={}){return new Promise((resolve,reject)=>{const p=spawn(command,args,{cwd,env:{...env,...extra},stdio:'inherit'});p.once('error',reject);p.once('exit',code=>code===0?resolve():reject(new Error(`${command} failed (${code})`)));});}
const mode=process.argv[2]||'check';
if(!['generate','check','acceptance','preview'].includes(mode))throw new Error('Unknown companion 3D operation');
const pixelAssets=path.join(root,'packages/desktop-resources/pet/models/candidates/pixel-actions');
if(mode==='preview'||mode==='acceptance'){
 const review=JSON.parse(await readFile(path.join(pixelAssets,'native-review.json'),'utf8'));
 const manifest=JSON.parse(await readFile(path.join(pixelAssets,'manifest.json'),'utf8'));
 if(!review.nativeIntegrationApproved||manifest.actions.some(a=>review.actionSHA256[a.id]!==a.sha256))throw new Error('Pixel model review does not match current assets.');
 await run(process.execPath,[path.join(root,'scripts/run-libby-actions.mjs'),'check']);
}
await run(process.execPath,[path.join(root,'scripts/generate-companion-models.mjs'),...(mode==='generate'?[]:['--check'])]);
if(mode!=='generate'){
 await run(process.execPath,[path.join(root,'scripts/generate-companion-native-reference.mjs'),'--check']);
 await run('go',['test','./internal/desktop/presentation/companion3d']);
 if(process.platform!=='darwin'){
  if(mode!=='check')throw new Error('The pilot renderer is currently macOS-only; Windows native integration remains pending.');
 }else{
  await run('go',['run','github.com/egoist/mygo/cmd/mygo','native','check','./internal/infrastructure/companion3d/metalbridge']);
  if(mode==='acceptance'){
   const captures=path.join(root,'target/native/pet3d-fidelity/native-captures');await mkdir(captures,{recursive:true});
   await run('go',['test','-tags','companion3d','./internal/desktop','-run','^TestCompanionPixelNative(Acceptance|Resources)$','-count=1','-v'],{LIBERTY_PET3D_ACCEPTANCE_DIR:captures,LIBERTY_PET3D_ASSET_DIR:pixelAssets});
  }
  if(mode==='preview'){
   const contents=path.join(root,'target/native/pet3d-fidelity/Libby Native Preview.app/Contents');
   const binary=path.join(contents,'MacOS/liberty-pet3d-preview');
   await mkdir(path.dirname(binary),{recursive:true});
   await writeFile(path.join(contents,'Info.plist'),'<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.westng.liberty.pet3d-preview</string><key>CFBundleName</key><string>Libby Native Preview</string><key>CFBundleExecutable</key><string>liberty-pet3d-preview</string><key>CFBundlePackageType</key><string>APPL</string><key>NSHighResolutionCapable</key><true/></dict></plist>');
   await run('go',['build','-tags','companion3d','-o',binary,'./cmd/liberty-pet3d-preview']);
   await mkdir(path.join(contents,'Frameworks'),{recursive:true});
   await copyFile(path.join(cwd,'.mygo/native',`darwin-${process.arch==='arm64'?'arm64':'amd64'}`,'libMetalbridge.dylib'),path.join(contents,'Frameworks/libMetalbridge.dylib'));
   await run(binary,['--assets',pixelAssets,...process.argv.slice(3)]);
  }
 }
}
