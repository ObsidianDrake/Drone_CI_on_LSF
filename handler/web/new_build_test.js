const assert = require('node:assert/strict');
// Execute the actual adapted parent callback as well as the form, retaining
// hook state across renders. No network or browser globals are needed.
let context;
function hooks() { return {slots:[], index:0, cleanups:[]}; }
function slot(init) { const i=context.index++; if (!(i in context.slots)) context.slots[i]=init(); return i; }
const n = {
  createElement:(type,props,...children)=>({type,props:{...props,children}}),
  useState:init=>{ const owner=context, i=slot(()=>init); return [owner.slots[i],value=>{owner.slots[i]=typeof value==='function'?value(owner.slots[i]):value}]; },
  useRef:init=>{ const i=slot(()=>({current:init})); return context.slots[i]; },
  useEffect:fn=>{const i=context.index++; if (!(i in context.slots)) { context.slots[i]=true; context.cleanups.push(fn()); }},
  useContext:()=>[{isRepoNavDisabled:false}], useMemo:fn=>fn(), useCallback:fn=>fn
};
const He={jsx:(type,props)=>({type,props}),jsxs:(type,props)=>({type,props}),Fragment:'fragment'};
const ii='form',li='section',oi={Input:'input'},Ye='button',mb=x=>x;
const kt={},o={a:x=>x},b={j:()=>({params:{namespace:'org',name:'qc'}}),h:()=>({search:''}),d:'switch',b:'route'};
const repo={namespace:'org',name:'qc',branch:'main',permissions:{write:true}};
const Ht=()=>({data:repo}),Cb=x=>x,Sb=()=>[],l={c:'link'},dh={Branches:'branches',Settings:'settings',Builds:'builds',Build:'build'};
const Wo='breadcrumb',Uo='crumb',Zo='icon',Nb='plus',Is='modal',jb=droneNewBuildForm,ol='not-found',S={};
const window={location:{pathname:base+'/org/qc'}};
let successes=[],refreshes=[],requests=[],resolveRequest,rejectRequest,closed=0;
const K=()=>({showSuccess:message=>successes.push(message),showError:()=>assert.fail('unexpected error toast')});
const Pt=(path,repository,build)=>{assert.equal(path,'/org/qc');assert.equal(repository,repo);refreshes.push(build)};
const at=(url,options)=>{requests.push({url,options});return new Promise((resolve,reject)=>{resolveRequest=resolve;rejectRequest=reject})};
function Ps(initial) { const state=n.useState(initial); return [state[0],()=>{closed++;state[1](!state[0])}]; }
const parent=eval('('+parentSource+')');
function render(fn,props,state) {context=state;state.index=0;return fn(props)}
function nodes(tree) { if (!tree || typeof tree!=='object') return []; return [tree,...[tree.props?.children].flat(Infinity).flatMap(nodes)]; }
function find(tree,type,predicate=()=>true) {const node=nodes(tree).find(node=>node.type===type&&predicate(node.props));assert.ok(node,'missing '+type);return node}
function content(tree) {return JSON.stringify(tree)}
function scenario() {
  successes=[];refreshes=[];requests=[];closed=0;
  const parentState=hooks(), formState=hooks();
  const tree=render(parent,{user:{admin:true}},parentState),modal=find(tree,Is),props=modal.props.children.props;
  let form;
  function draw() {return form=render(droneNewBuildForm,props,formState)}
  function edit(name,value) {find(form,'input',p=>p.name===name).props.onChange({target:{value}});draw()}
  function click(label) {find(form,'button',p=>p.children.includes(label)).props.onClick();draw()}
  function submit() {return form.props.onSubmit({preventDefault(){}})}
  draw();edit('branch','feature/test');edit('newKey','QC_MODE');edit('newVal','full');click('+ Add');
  // Keep an unfinished parameter too; errors must not clear any input.
  edit('newKey','EXTRA');edit('newVal','draft');
  return {draw,edit,submit,modal,formState,get form(){return form}};
}
(async()=>{
  for (const invalid of [null,undefined,{},[],{id:1},{number:21},{id:0,number:21},{id:1,number:0},{id:'1',number:21},{id:1,number:2.5}]) {
    const s=scenario(),waiting=s.submit();s.submit();s.draw();
    assert.equal(find(s.form,'fieldset').props.disabled,true);
    assert.equal(find(s.form,'button',p=>p.type==='submit').props.disabled,true);
    s.modal.props.hide();assert.equal(closed,0,'dialog closed during request');
    await Promise.resolve();assert.equal(requests.length,1,'duplicate request');
    assert.equal(requests[0].options.method,'POST');
    const url=new URL(requests[0].url,'https://example.test');
    assert.equal(url.pathname,'/api/repos/org/qc/builds');assert.equal(url.searchParams.get('branch'),'feature/test');assert.equal(url.searchParams.get('QC_MODE'),'full');
    assert.equal(url.searchParams.has('event'),false,'custom event behavior changed');
    resolveRequest(invalid);await waiting;s.draw();
    assert.equal(closed,0);assert.equal(successes.length,0);assert.equal(refreshes.length,0);
    const alert=find(s.form,'div',p=>p.role==='alert');
    assert.match(content(alert),/No build was created/);assert.match(content(alert),/feature\/test/);assert.match(content(alert),/Event: custom/);assert.match(content(alert),/trigger conditions/);
    assert.equal(find(s.form,'input',p=>p.name==='branch').props.value,'feature/test');
    assert.equal(find(s.form,'input',p=>p.name==='QC_MODE').props.value,'QC_MODE');
    assert.equal(find(s.form,'input',p=>p.name==='newVal').props.value,'draft');
    assert.equal(find(s.form,'button',p=>p.type==='submit').props.disabled,false);
  }
  const s=scenario();let waiting=s.submit();await Promise.resolve();rejectRequest(new Error('server unavailable'));await waiting;s.draw();
  assert.match(content(find(s.form,'div',p=>p.role==='alert')),/Unable to create build/);
  assert.equal(closed,0);assert.equal(successes.length,0);assert.equal(refreshes.length,0);
  assert.equal(find(s.form,'input',p=>p.name==='branch').props.value,'feature/test');
  assert.equal(find(s.form,'input',p=>p.name==='newVal').props.value,'draft');
  // Retry without rebuilding the form; success closes only after valid response.
  waiting=s.submit();s.draw();assert.equal(nodes(s.form).some(node=>node.props.role==='alert'),false);
  await Promise.resolve();const build={id:42,number:21,status:'pending'};resolveRequest(build);await waiting;await Promise.resolve();
  assert.equal(successes.length,1);assert.match(content(successes[0]),/Build #21 was created/);
  assert.match(content(successes[0]),/Branch: feature\/test · Event: custom/);
  assert.deepEqual(refreshes,[build]);assert.equal(closed,1);
  const empty=scenario();empty.edit('branch','');waiting=empty.submit();await Promise.resolve();resolveRequest(null);await waiting;empty.draw();
  assert.match(content(find(empty.form,'div',p=>p.role==='alert')),/main/);
  empty.modal.props.hide();assert.equal(closed,1,'dialog could not close after request');
  // Navigating away while pending must not update or toggle an unmounted form.
  const gone=scenario();waiting=gone.submit();await Promise.resolve();gone.formState.cleanups.forEach(fn=>fn&&fn());resolveRequest(null);await waiting;assert.equal(closed,0);
})().catch(error=>{console.error(error);process.exitCode=1});
