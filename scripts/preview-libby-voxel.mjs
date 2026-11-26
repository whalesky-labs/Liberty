// Local, read-only asset review. This does not start or replace the native pet.
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const output=path.join(root,'target/native/pet3d-fidelity');
const routes=new Map([
  ['/',path.join(root,'scripts/blender/libby-voxel-review.html')],
  ['/model.glb',path.join(output,'voxel/libby-voxel.glb')],
  ['/source.blend',path.join(output,'voxel/libby-voxel.blend')],
]);
for(let frame=1;frame<=9;frame++)routes.set(`/reference/${frame}.png`,path.join(root,`packages/desktop-resources/images/pet/action/crush/crush_${String(frame).padStart(2,'0')}.png`));
for(const file of ['build/three.module.js','build/three.core.js','examples/jsm/loaders/GLTFLoader.js','examples/jsm/controls/OrbitControls.js','examples/jsm/utils/BufferGeometryUtils.js'])routes.set('/vendor/'+file,path.join(output,'viewer/vendor',file));
const mime={'.html':'text/html; charset=utf-8','.js':'text/javascript; charset=utf-8','.png':'image/png','.glb':'model/gltf-binary','.blend':'application/octet-stream'};
for(const file of routes.values())if(!fs.existsSync(file))throw new Error(`Missing review input: ${path.relative(root,file)}`);
const server=http.createServer((request,response)=>{
  const file=routes.get(new URL(request.url,'http://127.0.0.1').pathname);
  if(!['GET','HEAD'].includes(request.method)||!file){response.writeHead(404);response.end();return;}
  response.writeHead(200,{'Content-Type':mime[path.extname(file)],'Cache-Control':'no-store','X-Content-Type-Options':'nosniff','Content-Security-Policy':"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'"});
  if(request.method==='HEAD'){response.end();return;}
  fs.createReadStream(file).on('error',()=>response.destroy()).pipe(response);
});
server.listen(43187,'127.0.0.1',()=>console.log('Libby voxel review: http://127.0.0.1:43187 (paused by default)'));
for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>server.close(()=>process.exit(0)));
