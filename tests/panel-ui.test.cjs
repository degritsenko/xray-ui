// Exercise the shipped panel script with a small DOM model and controlled API promises.
// No browser, network, or third-party dependencies are required.
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const path=require('node:path');
class Element {
 constructor(tag='div'){this.tagName=tag;this.hidden=false;this.disabled=false;this.value='';this.textContent='';this.children=[];this.listeners={};this.attributes={};this.style={};this.classList={toggle(){}};}
 append(...children){this.children.push(...children);}
 replaceChildren(...children){this.children=children;}
 setAttribute(name,value){this.attributes[name]=value;}
 addEventListener(type,callback){this.listeners[type]=callback;}
 fire(type='click'){assert.equal(this.disabled,false,'control must remain enabled');return this.listeners[type]?.({preventDefault(){}});}
}
const flush=async()=>{for(let i=0;i<8;i++)await new Promise(setImmediate)};
function fixture(fixtureOptions={}){
 const nodes=[{id:'one',name:'First',server:'first.example.com',port:443,probe:{reachable:true,milliseconds:200,checkedAt:new Date().toISOString()}},{id:'two',name:'Second',server:'second.example.com',port:443,probe:{reachable:true,milliseconds:30,checkedAt:new Date().toISOString()}}];
 const state={subscriptions:[{id:'sub',name:'VPN',nodes,updatedAt:'2026-10-28T00:00:00Z',error:''}],active:{subscriptionID:'sub',node:nodes[0],enabled:true},running:true,probing:false,logs:[],csrf:'test-csrf',version:'test',coreVersion:'26.9.9',probeIntervalSeconds:600,nextProbeAt:'2026-10-29T12:00:00Z'};
 if(fixtureOptions.sameHost){state.subscriptions[0].host='provider.example';state.subscriptions[0].preferenceID='feed-a';state.subscriptions.push({...structuredClone(state.subscriptions[0]),id:'sub-two',preferenceID:'feed-b'})}
 if(fixtureOptions.usage)state.subscriptions[0].usage=fixtureOptions.usage;
 if(fixtureOptions.failedProbe)nodes[0].probe=fixtureOptions.failedProbe;
 const all=[],ids=new Map();
 const document={hidden:false,getElementById(id){if(!ids.has(id)){const e=new Element(/toggle|restart|logout|refresh-source|probe-servers|login-button/.test(id)?'button':'div');e.id=id;ids.set(id,e);all.push(e)}return ids.get(id)},createElement(tag){const e=new Element(tag);all.push(e);return e},querySelectorAll(){return all.filter(e=>e.tagName==='button'&&e.id!=='login-button')}};
 const calls=[];let finishProbe,deferState=false,releaseState;
 const response=(data={},status=200)=>({ok:status<400,status,json:async()=>data});
 const fetch=async(url,options={})=>{
  calls.push(url);
  if(url==='/api/probe'){
   state.probing=true;
   return new Promise((resolve,reject)=>{finishProbe=()=>{state.probing=false;resolve(response())};options.signal?.addEventListener('abort',()=>{state.probing=false;const e=new Error('aborted');e.name='AbortError';reject(e)},{once:true})});
  }
  if(fixtureOptions.refreshFails&&url.endsWith('/refresh')){state.subscriptions[0].error='could not download subscription';return response({error:'could not download subscription',scope:'subscription'},400)}
  if(url==='/api/xray/stop'){state.running=false;state.active.enabled=false;}
  if(url==='/api/activate'){state.active.node=nodes.find(n=>n.id===JSON.parse(options.body).nodeID);state.running=true;state.active.enabled=true;}
  if(url==='/api/state'&&deferState){deferState=false;const snapshot=structuredClone(state);await new Promise(resolve=>releaseState=resolve);return response(snapshot)}
  return response(url==='/api/state'?structuredClone(state):{});
 };
 vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../internal/app/web/app.js'),'utf8'),{document,fetch,setInterval(){},AbortController});
 return {document,all,ids,calls,state,finish:()=>finishProbe(),deferState:()=>deferState=true,releaseState:()=>releaseState()};
}
test('server checks leave Connect and Stop usable while the probe request is pending',async()=>{
 const f=fixture();await flush();
 const checking=f.ids.get('probe-servers').fire();await flush();
 assert.equal(f.ids.get('probe-servers').disabled,true,'duplicate checks must be disabled');
 assert.equal(f.ids.get('probe-servers').textContent,'Checking…');
 assert.equal(f.ids.get('core-toggle').disabled,false,'Stop blocked by server check');
 assert.equal(f.ids.get('logout').disabled,false,'Sign out blocked by server check');
 // Only currently rendered controls are in the active node-list tree.
 const root=f.ids.get('server-list');const walk=e=>[e,...e.children.flatMap(walk)];
 const connect=walk(root).find(e=>e.tagName==='button'&&e.textContent==='Connect');
 assert.equal(connect.disabled,false,'Connect blocked by server check');
 await connect.fire();assert.ok(f.calls.includes('/api/activate'));
 await f.ids.get('core-toggle').fire();assert.ok(f.calls.includes('/api/xray/stop'));
 f.finish();await checking;
 assert.equal(f.ids.get('probe-servers').disabled,false);
});
test('signing out cancels a pending probe and keeps the login screen visible',async()=>{
 const f=fixture();await flush();const checking=f.ids.get('probe-servers').fire();await flush();
 await f.ids.get('logout').fire();await checking;
 assert.equal(f.ids.get('app').hidden,true);
 assert.equal(f.ids.get('login-screen').hidden,false);
});

test('logout must ignore a state response already in flight after probe finishes',async()=>{
 const f=fixture();await flush();const checking=f.ids.get('probe-servers').fire();await flush();
 f.deferState();f.finish();await flush();
 await f.ids.get('logout').fire();
 assert.equal(f.ids.get('app').hidden,true,'logout must show login immediately');
 f.releaseState();await checking;
 assert.equal(f.ids.get('app').hidden,true,'stale state response reopened authenticated panel after logout');
});

test('sign-in uses a native named form with password-manager autocomplete hints',()=>{
 const html=fs.readFileSync(path.join(__dirname,'../internal/app/web/index.html'),'utf8');
 const form=html.match(/<form\b[^>]*id="login-form"[^>]*>/)?.[0]||'';
 assert.match(form,/method="post"/);assert.match(form,/action="\/login"/);
 const password=html.match(/<input\b[^>]*id="password"[^>]*>/)?.[0]||'';
 assert.match(password,/name="password"/);assert.match(password,/autocomplete="current-password"/);
 // This is a sign-in field, not a new-password form. It must also allow
 // older stored credentials and all 32 Unicode-code-point configured passwords.
 const maximum=Number(password.match(/maxlength="(\d+)"/)?.[1]);
 assert.ok(maximum>=256);assert.ok('🔑'.repeat(32).length<=maximum);
 assert.doesNotMatch(password,/minlength=/);
 assert.match(html,/<input\b[^>]*name="username"[^>]*autocomplete="username"/);
 const script=fs.readFileSync(path.join(__dirname,'../internal/app/web/app.js'),'utf8');
 assert.doesNotMatch(script,/\$\('login-form'\)\.addEventListener\('submit'/,'native submit must reach the browser');
});

test('latency sort and favorites filter preserve manual connection selection',async()=>{
 const f=fixture();await flush();
 const root=f.ids.get('server-list');const walk=e=>[e,...e.children.flatMap(walk)];
 f.state.subscriptions[0].nodes[0].probe={reachable:true,milliseconds:200,checkedAt:new Date().toISOString()};
 f.state.subscriptions[0].nodes[1].probe={reachable:true,milliseconds:30,checkedAt:new Date().toISOString()};
 const sort=f.document.getElementById('server-sort');sort.value='latency';await sort.fire('change');
 let names=walk(root).filter(e=>e.className==='server-name').map(e=>e.textContent);
 assert.deepEqual(names,['Second','First']);
 const star=walk(root).find(e=>e.tagName==='button'&&e.attributes['aria-label']==='Add Second to favorites');
 assert.ok(star,'favorite action missing');await star.fire();
 await f.document.getElementById('filter-favorites').fire();
 names=walk(root).filter(e=>e.className==='server-name').map(e=>e.textContent);
 assert.deepEqual(names,['Second']);
 assert.ok(!f.calls.includes('/api/activate'),'presentation changes switched the VPN');
});

test('same-host subscriptions keep independent favorites and collapse state',async()=>{
 const f=fixture({sameHost:true});await flush();
 const walk=e=>[e,...e.children.flatMap(walk)],root=f.ids.get('server-list');
 let buttons=walk(root).filter(e=>e.tagName==='button'&&e.attributes['aria-label']==='Add First to favorites');
 assert.equal(buttons.length,2);await buttons[0].fire();
 assert.equal(walk(root).filter(e=>e.className==='favorite selected').length,1);
 await walk(root).find(e=>e.className==='subscription-title').fire();
 const headings=walk(root).filter(e=>e.className==='subscription-title');
 assert.equal(headings[0].attributes['aria-expanded'],'false');assert.equal(headings[1].attributes['aria-expanded'],'true');
});

test('subscription refresh failure is shown locally without a global notification',async()=>{
 const f=fixture({refreshFails:true});await flush();
 const walk=e=>[e,...e.children.flatMap(walk)],root=f.ids.get('server-list');
 const refresh=walk(root).find(e=>e.tagName==='button'&&e.attributes['aria-label']==='Refresh VPN');
 await refresh.fire();
 assert.equal(f.ids.get('notice').hidden,true,'subscription error duplicated at top');
 assert.ok(walk(root).some(e=>e.className==='source-error'&&e.textContent==='could not download subscription'));
});
test('provider usage and expiry render only reported values',async()=>{
 const walk=e=>[e,...e.children.flatMap(walk)];
 const f=fixture({usage:{upload:100,download:200,total:1000,expire:1798761600}});await flush();
 let elements=walk(f.ids.get('server-list'));
 assert.ok(elements.some(e=>e.textContent==='300 B / 1 KB'),'traffic quota missing');
 assert.ok(elements.some(e=>e.textContent.startsWith('Expires ')&&e.textContent.includes('2027')),'expiry missing');
 const progress=elements.find(e=>e.attributes.role==='progressbar');assert.equal(progress.attributes['aria-valuenow'],'30');
 const unlimited=fixture({usage:{download:0,total:0,expire:0}});await flush();
 elements=walk(unlimited.ids.get('server-list'));
 assert.ok(elements.some(e=>e.textContent==='0 B used · Unlimited'));
 assert.ok(elements.some(e=>e.textContent==='No expiry'));
 assert.ok(!elements.some(e=>e.attributes.role==='progressbar'));
 const absent=fixture();await flush();
 assert.ok(!walk(absent.ids.get('server-list')).some(e=>e.className==='subscription-usage'));
});

test('failed check retains last successful delay and exposes its age and failure reason',async()=>{
 const last={milliseconds:200,checkedAt:new Date(Date.now()-60000).toISOString()};
 const f=fixture({failedProbe:{reachable:false,milliseconds:0,error:'TLS handshake timed out',checkedAt:new Date().toISOString(),lastSuccess:last}});await flush();
 assert.equal(f.ids.get('active-latency').textContent,'200');
 assert.match(f.ids.get('active-checked').textContent,/Last success/);
 assert.match(f.ids.get('active-probe-error').textContent,/TLS handshake timed out/);
 const walk=e=>[e,...e.children.flatMap(walk)];
 assert.ok(walk(f.ids.get('server-list')).some(e=>e.className==='last-success'&&e.textContent.includes('200 ms')));
 const firstFailure=fixture({failedProbe:{reachable:false,milliseconds:0,error:'Server DNS lookup failed',checkedAt:new Date().toISOString()}});await flush();
 assert.equal(firstFailure.ids.get('active-latency').textContent,'—','invented a successful result');
});
test('availability strip shows exactly five checks with newest on the right',async()=>{
 const f=fixture({failedProbe:{reachable:false,checkedAt:new Date().toISOString(),history:[true,false,true]}});await flush();
 const walk=e=>[e,...e.children.flatMap(walk)];
 const strips=walk(f.ids.get('server-list')).filter(e=>e.className==='probe-history');
 assert.ok(strips.length>0);
 assert.deepEqual(strips[0].children.map(e=>e.className),['history-segment','history-segment','history-segment passed','history-segment failed','history-segment passed']);
 assert.equal(strips[0].attributes['role'],'img');
 assert.match(strips[0].attributes['aria-label'],/No data, No data, Passed, Failed, Passed/);
 for(const strip of strips)assert.equal(strip.children.length,5);
});
