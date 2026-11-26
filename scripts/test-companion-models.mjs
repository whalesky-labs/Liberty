import test from 'node:test';
import assert from 'node:assert/strict';
import {makePilot,encodeGLB,pilots,actions} from './lib/companion-model.mjs';
test('reference pilot meshes are finite, three-dimensional, articulated and deterministic',()=>{
 assert.equal(actions.length,15);assert.equal(pilots.length,3);
 for(const name of pilots){
  const model=makePilot(name);const bytes=encodeGLB(model);assert.deepEqual(bytes,encodeGLB(makePilot(name)));
  const triangles=model.meshes.reduce((sum,m)=>sum+m.idx.length/3,0);assert.ok(triangles>8000&&triangles<15000);
  for(const mesh of model.meshes){assert.equal(mesh.p.length,mesh.n.length);assert.ok(mesh.p.every(Number.isFinite));assert.ok(mesh.n.every(Number.isFinite));assert.ok(mesh.idx.every(i=>Number.isInteger(i)&&i>=0&&i<mesh.p.length/3));}
  assert.ok(model.tracks.some(t=>t.path==='rotation'));
  for(const track of model.tracks){assert.equal(track.times.length,track.values.length);assert.equal(track.times[0],0);assert.equal(track.times.at(-1),model.duration);assert.ok(track.values.flat().every(Number.isFinite));}
  assert.equal(bytes.readUInt32LE(8),bytes.length);
 }
 assert.throws(()=>makePilot('snow'));
});
