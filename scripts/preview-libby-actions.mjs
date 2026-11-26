// Loopback-only, read-only viewer of all original action assets.
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const assetRoot=path.join(root,'packages/desktop-resources/pet/models/candidates/pixel-actions');
const manifest=JSON.parse(fs.readFileSync(path.join(assetRoot,'manifest.json'),'utf8'));
const routes=new Map([['/',path.join(root,'scripts/blender/libby-actions-review.html')],['/manifest.json',path.join(assetRoot,'manifest.json')]]);
for(const action of manifest.actions){routes.set('/models/'+action.id+'.glb',path.join(assetRoot,action.file));routes.set('/sources/'+action.id+'.blend',path.join(assetRoot,action.source));for(const ref of action.references)routes.set(`/reference/${action.id}/${ref.frame}.png`,path.join(root,'packages/desktop-resources',ref.file));}
for(const file of ['build/three.module.js','build/three.core.js','examples/jsm/loaders/GLTFLoader.js','examples/jsm/controls/OrbitControls.js','examples/jsm/utils/BufferGeometryUtils.js'])routes.set('/vendor/'+file,path.join(root,'target/native/pet3d-fidelity/viewer/vendor',file));
for(const file of routes.values())if(!fs.existsSync(file))throw Error('Missing viewer input: '+path.relative(root,file));
const mime={'.html':'text/html; charset=utf-8','.json':'application/json','.js':'text/javascript; charset=utf-8','.png':'image/png','.glb':'model/gltf-binary','.blend':'application/octet-stream'};
const server=http.createServer((req,res)=>{const file=routes.get(new URL(req.url,'http://127.0.0.1').pathname);if(!['GET','HEAD'].includes(req.method)||!file){res.writeHead(404);res.end();return;}res.writeHead(200,{'Content-Type':mime[path.extname(file)],'Cache-Control':'no-store','X-Content-Type-Options':'nosniff','Content-Security-Policy':"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'"});if(req.method==='HEAD')res.end();else fs.createReadStream(file).on('error',()=>res.destroy()).pipe(res);});
server.listen(43187,'127.0.0.1',()=>console.log('Libby all-actions review: http://127.0.0.1:43187'));
for(const signal of ['SIGINT','SIGTERM'])process.on(signal,()=>server.close(()=>process.exit(0)));
