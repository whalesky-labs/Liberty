// Rejected visual pilot, retained only as a GPU rendering fixture.
// The user rejected its proportions and likeness on 2026-11-25. Do not use
// these primitive-built shapes as the source of the replacement character.
export const pilots = ['crush', 'rope', 'work'];
export const actions = ['crush','defecate','drive','eat','gaming','pants','reading','rope','run','slack','sleep','snow','studying','toy','work'];
export function quat(x=0,y=0,z=0) {
 const a=x/2,b=y/2,c=z/2,s=Math.sin,k=Math.cos;
 return [s(a)*k(b)*k(c)-k(a)*s(b)*s(c),k(a)*s(b)*k(c)+s(a)*k(b)*s(c),k(a)*k(b)*s(c)-s(a)*s(b)*k(c),k(a)*k(b)*k(c)+s(a)*s(b)*s(c)];
}
const colors={wool:'#f1ece0',shade:'#d8d0bd',skin:'#ffcc9c',horn:'#956343',dark:'#493329',eye:'#241d21',white:'#fff7e9',hair:'#71513b',pink:'#df5969',cheek:'#ef9286',pants:'#d6e5e4',wood:'#a87757',computer:'#49535c',screen:'#b7d4d5'};
function rgb(key){return [1,3,5].map(i=>{const v=parseInt((colors[key]||key).slice(i,i+2),16)/255;return v<=.04045?v/12.92:((v+.055)/1.055)**2.4;});}
export function ellipsoid(size,segments=14,rings=9) {
 const p=[],n=[],idx=[];
 for(let y=0;y<=rings;y++)for(let x=0;x<=segments;x++){
  const a=y/rings*Math.PI,b=x/segments*Math.PI*2,v=[Math.sin(a)*Math.cos(b),Math.cos(a),Math.sin(a)*Math.sin(b)];
  p.push(...v.map((q,i)=>q*size[i]));const normal=v.map((q,i)=>q/size[i]),d=Math.hypot(...normal);n.push(...normal.map(q=>q/d));
  if(y<rings&&x<segments){const i=y*(segments+1)+x;if(y>0)idx.push(i,i+1,i+segments+1);if(y<rings-1)idx.push(i+1,i+segments+2,i+segments+1);}
 }return {p,n,idx};
}
export function cuboid(size) {
 const p=[],n=[],idx=[];
 for(const [normal,u,v]of [[[1,0,0],[0,1,0],[0,0,1]],[[-1,0,0],[0,0,1],[0,1,0]],[[0,1,0],[0,0,1],[1,0,0]],[[0,-1,0],[1,0,0],[0,0,1]],[[0,0,1],[1,0,0],[0,1,0]],[[0,0,-1],[0,1,0],[1,0,0]]]){
  const i=p.length/3;for(const [a,b]of [[-1,-1],[1,-1],[1,1],[-1,1]]){p.push(...normal.map((q,k)=>(q+a*u[k]+b*v[k])*size[k]));n.push(...normal);}idx.push(i,i+1,i+2,i,i+2,i+3);
 }return {p,n,idx};
}
export function tube(points,radius) {
 const p=[],n=[],idx=[],segments=8;
 points.forEach((point,i)=>{
  const prev=points[Math.max(0,i-1)],next=points[Math.min(points.length-1,i+1)];let t=next.map((v,k)=>v-prev[k]);const length=Math.hypot(...t);t=t.map(v=>v/length);
  let u=Math.abs(t[1])<.9?[t[2],0,-t[0]]:[0,-t[2],t[1]];const d=Math.hypot(...u);u=u.map(v=>v/d);const v=[t[1]*u[2]-t[2]*u[1],t[2]*u[0]-t[0]*u[2],t[0]*u[1]-t[1]*u[0]];
  for(let j=0;j<=segments;j++){const a=j/segments*Math.PI*2,normal=u.map((q,k)=>q*Math.cos(a)+v[k]*Math.sin(a));p.push(...point.map((q,k)=>q+normal[k]*radius));n.push(...normal);if(i<points.length-1&&j<segments){const z=i*(segments+1)+j;idx.push(z,z+segments+1,z+1,z+1,z+segments+1,z+segments+2);}}
 });return {p,n,idx};
}
function heart(size) {
 const p=[],n=[],idx=[];
 for(let side=0;side<2;side++){const base=p.length/3,z=side?-.015:.015; p.push(0,0,z);n.push(0,0,side?-1:1);
  for(let i=0;i<=32;i++){const t=i/32*Math.PI*2;p.push(16*Math.sin(t)**3*size/16,(13*Math.cos(t)-5*Math.cos(2*t)-2*Math.cos(3*t)-Math.cos(4*t))*size/16,z);n.push(0,0,side?-1:1);if(i)idx.push(base,base+i+(side?0:1),base+i+(side?1:0));}
 }for(let i=1;i<=32;i++)idx.push(i,i+34,i+1,i+1,i+34,i+35);return {p,n,idx};
}
export function makePilot(action) {
 if(!pilots.includes(action))throw new Error('Action has no pilot model');
 const nodes=[],meshes=[],materials=[],materialIDs=new Map(),tracks=[];
 const node=(name,parent=-1,translation=[0,0,0],rotation=[0,0,0])=>{const id=nodes.length;nodes.push({name,translation,rotation:quat(...rotation),children:[]});if(parent>=0)nodes[parent].children.push(id);return id;};
 function shape(name,parent,geometry,translation,color,rotation=[]){
  const id=node(name,parent,translation,rotation);if(!materialIDs.has(color)){materialIDs.set(color,materials.length);materials.push({name:color,pbrMetallicRoughness:{baseColorFactor:[...rgb(color),1],metallicFactor:0,roughnessFactor:.85}});}
  nodes[id].mesh=meshes.length;meshes.push({...geometry,material:materialIDs.get(color)});return id;
 }
 const ball=(name,parent,size,position,color,rotation=[],detail=14)=>shape(name,parent,ellipsoid(size,detail,Math.max(5,Math.round(detail*.65))),position,color,rotation);
 const box=(name,parent,size,position,color,rotation=[])=>shape(name,parent,cuboid(size),position,color,rotation);
 const track=(id,path,fn)=>tracks.push({id,path,fn});
 const root=node('root'),body=node('torso',root,[0,.54,0]),head=node('head',body,[0,.62,.015]);
 ball('body-wool',body,[.31,.39,.23],[0,0,0],'wool');ball('belly',body,[.255,.25,.075],[0,-.015,.205],'skin');ball('shorts',body,[.305,.16,.23],[0,-.28,.005],'pants');
 ball('hood',head,[.62,.64,.38],[0,.02,-.035],'wool',[],22);
 for(let i=0;i<14;i++){const a=Math.PI*2*i/14;ball('wool-lock-'+i,head,[.145,.15,.13],[Math.sin(a)*.53,Math.cos(a)*.52,.09],'wool',[],9);}
 ball('face',head,[.43,.445,.135],[0,-.04,.315],'skin',[],22);
 const eyes=[];
 for(const side of [-1,1]){
  ball('ear-'+side,head,[.18,.105,.095],[side*.58,.08,.2],'wool',[0,0,side*.2]);ball('ear-inner-'+side,head,[.115,.05,.04],[side*.63,.075,.275],'skin');
  for(let i=0;i<4;i++)ball('horn-'+side+'-'+i,head,[.12-i*.018,.095-i*.01,.09-i*.014],[side*(.47+i*.071),.49+i*.035,-.055-i*.015],'horn',[0,0,-side*.4],10);
  ball('cheek-'+side,head,[.085,.045,.016],[side*.26,-.135,.439],'cheek');ball('brow-'+side,head,[.067,.023,.02],[side*.17,.20,.422],'hair',[0,0,side*.15]);
  const eye=node('eye-'+side,head,[side*.17,.045,.452]);eyes.push(eye);
  if(action==='crush'){shape('heart-eye-'+side,eye,heart(.078),[0,0,.018],'pink');}else{
   ball('eye-white-'+side,eye,[.062,.078,.023],[0,0,0],'white');ball('pupil-'+side,eye,[.041,.055,.018],[0,0,.023],'eye');ball('glint-'+side,eye,[.012,.017,.006],[-.012,.018,.04],'white',[],8);
  }
 }
 for(let i=0;i<5;i++)ball('fringe-'+i,head,[.09,.065,.035],[(i-2)*.125,.29+(i===2?.04:0),.405],'hair');
 ball('nose',head,[.025,.018,.018],[0,-.09,.456],'skin');
 shape('mouth',head,tube([[-.07,.018,0],[-.035,-.01,.01],[0,-.018,.013],[.035,-.01,.01],[.07,.018,0]],.013),[0,-.2,.448],'dark');
 const arms=[],hands=[],legs=[];
 for(const side of [-1,1]){
  const arm=node('arm-'+side,body,[side*.295,.19,.025]);arms.push(arm);ball('upper-arm-'+side,arm,[.085,.16,.09],[0,-.1,0],'skin');
  const forearm=node('forearm-'+side,arm,[0,-.21,0]);ball('forearm-mesh-'+side,forearm,[.085,.12,.085],[0,-.075,0],'skin');
  const hand=node('hand-'+side,forearm,[0,-.19,.015]);hands.push(hand);ball('hand-mesh-'+side,hand,[.1,.105,.075],[0,0,0],'skin');ball('thumb-'+side,hand,[.044,.06,.05],[-side*.07,.025,.025],'skin');
  const leg=node('leg-'+side,root,[side*.15,.27,0]);legs.push(leg);ball('leg-skin-'+side,leg,[.11,.12,.10],[0,-.05,0],'skin');ball('foot-'+side,leg,[.125,.065,.17],[0,-.17,.065],'horn');
 }
 track(body,'translation',t=>[0,.54+Math.sin(t*2*Math.PI)*.012,0]);
 if(action==='crush'){
  for(const [i,arm]of arms.entries())track(arm,'rotation',t=>quat(-.5+Math.sin(t*Math.PI*2)*.04,0,(i===0?-1:1)*(2.45+Math.sin(t*Math.PI*2)*.06)));
  track(head,'rotation',t=>quat(.015,Math.sin(t*Math.PI*2)*.07,Math.sin(t*Math.PI*2)*.065));
 }
 if(action==='rope'){
  nodes[arms[0]].rotation=quat(0,0,-.9);nodes[arms[1]].rotation=quat(0,0,.9);
  for(const hand of hands)box('rope-handle-'+hand,hand,[.025,.105,.028],[0,-.045,.04],'horn');
  const rope=node('rope-swing',root,[0,.43,.055]);
  shape('rope-cord',rope,tube(Array.from({length:49},(_,i)=>{const a=i/48*Math.PI*2;return [Math.cos(a)*.7,Math.sin(a)*.88,0];}),.016),[0,0,0],'shade');
  track(rope,'rotation',t=>quat(t*Math.PI*2,0,0));track(root,'translation',t=>[0,Math.max(0,Math.sin(t*Math.PI*2))*.22,0]);
  for(const leg of legs)track(leg,'rotation',t=>quat(.1+Math.max(0,-Math.sin(t*Math.PI*2))*.45,0,0));
 }
 if(action==='work'){
  nodes[root].rotation=quat(0,-.12,0);nodes[head].rotation=quat(.13,0,0);
  // Replace breathing track with the seated position.
  tracks.length=0;track(body,'translation',t=>[0,.64+Math.sin(t*Math.PI*2)*.008,-.12]);
  for(const leg of legs){nodes[leg].translation[1]=.37;nodes[leg].rotation=quat(-.65,0,0);}
  box('chair-seat',root,[.35,.045,.31],[0,.4,-.2],'wood');box('chair-back',root,[.35,.4,.05],[0,.85,-.48],'wood');
  for(const x of [-.29,.29])for(const z of [-.44,.04])box('chair-leg-'+x+z,root,[.035,.2,.035],[x,.2,z],'dark');
  box('desk-top',root,[.62,.035,.31],[0,.56,.52],'wood');for(const x of [-.53,.53])for(const z of [.28,.76])box('desk-leg-'+x+z,root,[.035,.26,.035],[x,.26,z],'dark');
  box('laptop-base',root,[.37,.018,.22],[0,.613,.48],'computer');
  for(let y=0;y<3;y++)for(let x=0;x<8;x++)box('key-'+x+'-'+y,root,[.03,.004,.018],[(x-3.5)*.075,.635,.35+y*.06],'shade');
  const laptop=node('laptop-display',root,[0,.63,.68],[-.19,0,0]);box('display-back',laptop,[.37,.24,.019],[0,.24,0],'computer');box('display-panel',laptop,[.326,.185,.003],[0,.255,-.022],'screen');
  for(const [i,arm]of arms.entries())track(arm,'rotation',t=>quat(-1.3+Math.sin(t*Math.PI*6+i*Math.PI)*.06,0,i===0?-.15:.15));
  track(head,'rotation',t=>quat(.13+Math.sin(t*Math.PI*2)*.04,Math.sin(t*Math.PI*2)*.025,0));
 }
 if(action!=='crush')for(const eye of eyes)track(eye,'scale',t=>[1,Math.abs(t-.82)<.045?.12:1,1]);
 const duration=action==='rope'?1.15:action==='work'?3:2.8;
 return {action,nodes,meshes,materials,duration,tracks:tracks.map(({id,path,fn})=>{
  const times=Array.from({length:33},(_,i)=>i/32*duration),values=times.map((_,i)=>fn(i/32));
  if(path==='rotation')for(let i=1;i<values.length;i++)if(values[i].reduce((sum,v,k)=>sum+v*values[i-1][k],0)<0)values[i]=values[i].map(v=>-v);
  return {id,path,times,values};
 })};
}

export function encodeGLB(model) {
 const chunks=[],views=[],accessors=[];let offset=0;
 function accessor(values,type,componentType=5126,target){
  const stride={SCALAR:1,VEC3:3,VEC4:4}[type],buffer=Buffer.alloc(values.length*(componentType===5123?2:4));
  values.forEach((v,i)=>componentType===5123?buffer.writeUInt16LE(v,i*2):buffer.writeFloatLE(v,i*4));
  const padded=Buffer.alloc(Math.ceil(buffer.length/4)*4);buffer.copy(padded);const view={buffer:0,byteOffset:offset,byteLength:buffer.length};if(target)view.target=target;
  const a={bufferView:views.length,componentType,count:values.length/stride,type};views.push(view);chunks.push(padded);offset+=padded.length;
  if(type==='VEC3'||type==='SCALAR'){a.min=Array.from({length:stride},(_,k)=>values.reduce((min,v,i)=>i%stride===k?Math.min(min,v):min,Infinity));a.max=Array.from({length:stride},(_,k)=>values.reduce((max,v,i)=>i%stride===k?Math.max(max,v):max,-Infinity));}
  accessors.push(a);return accessors.length-1;
 }
 const meshes=model.meshes.map(g=>({primitives:[{attributes:{POSITION:accessor(g.p,'VEC3',5126,34962),NORMAL:accessor(g.n,'VEC3',5126,34962)},indices:accessor(g.idx,'SCALAR',5123,34963),material:g.material}]}));
 const samplers=[],channels=[];for(const t of model.tracks){channels.push({sampler:samplers.length,target:{node:t.id,path:t.path}});samplers.push({input:accessor(t.times,'SCALAR'),output:accessor(t.values.flat(),t.path==='rotation'?'VEC4':'VEC3'),interpolation:'LINEAR'});}
 const nodes=model.nodes.map(n=>{const result={...n};if(!result.children.length)delete result.children;return result;});
 const doc={asset:{version:'2.0',generator:'Liberty rejected render fixture',extras:{status:'rejected',referenceAction:model.action}},scene:0,scenes:[{nodes:[0]}],nodes,meshes,materials:model.materials,animations:[{name:model.action,samplers,channels}],buffers:[{byteLength:offset}],bufferViews:views,accessors};
 const raw=Buffer.from(JSON.stringify(doc)),json=Buffer.alloc(Math.ceil(raw.length/4)*4,32);raw.copy(json);const bin=Buffer.concat(chunks),out=Buffer.alloc(28+json.length+bin.length);
 out.writeUInt32LE(0x46546c67,0);out.writeUInt32LE(2,4);out.writeUInt32LE(out.length,8);out.writeUInt32LE(json.length,12);out.writeUInt32LE(0x4e4f534a,16);json.copy(out,20);const b=20+json.length;out.writeUInt32LE(bin.length,b);out.writeUInt32LE(0x004e4942,b+4);bin.copy(out,b+8);return out;
}
