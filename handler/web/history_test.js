const assert = require('assert');
const droneHistoryCSS = '';
const document={activeElement:null,body:{style:{overflow:''}}};
let states=[], cursor=0, effects=[], deps=[], pending=[], calls=[], confirmation=true, conflict=false, notices=[];
const n={
 createElement:(type,props,...children)=>({type,props:props||{},children}),Fragment:'fragment',
 useRef(initial){let i=cursor++;if(!(i in states))states[i]={current:initial};return states[i];},
 useState(initial){let i=cursor++;if(!(i in states))states[i]=initial;return [states[i],v=>{states[i]=typeof v==='function'?v(states[i]):v}];},
 useEffect(fn,values){let i=cursor++;if(!deps[i]||values.some((v,k)=>v!==deps[i][k])){deps[i]=values;pending.push(()=>{if(effects[i])effects[i]();effects[i]=fn()});}}
};
const b={j:()=>({params:{namespace:'team',name:'repo'},url:'/team/repo'}),g:()=>history};
const history={replace:()=>{}}, Gu=x=>x, A=()=>{}, Zu='summary', Mu='build-list', Ye='button';
const window={confirm:message=>{notices.push(message);return confirmation}};
const realTimeout=setTimeout;
global.setInterval=()=>1;global.clearInterval=()=>{};
const first=Array.from({length:25},(_,i)=>({number:50-i,status:i===0?'running':i===1?'success':i===2?'blocked':'failure'}));
function at(url,options){
 calls.push({url,options});
 if(!options)return Promise.resolve(url.includes('?page=2&')?[{number:1,status:'error'}]:first);
 if(options.data.commit){if(conflict)return Promise.reject(new Error('Build history changed. Preview and confirm the selection again.'));return Promise.resolve({numbers:options.data.numbers||[1]});}
 let rows=options.data.numbers?first.concat([{number:1,status:'error'}]).filter(x=>options.data.numbers.includes(x.number)&&['success','failure','error','killed','skipped'].includes(x.status)&&(!options.data.non_success||x.status!=='success')).map(x=>x.number):options.data.non_success?first.concat([{number:1,status:"error"}]).filter(x=>["failure","error","killed","skipped"].includes(x.status)).map(x=>x.number):[1];
 return Promise.resolve({numbers:rows,token:'preview'});
}
let tree,admin=true;
function render(){cursor=0;tree=qu({user:{admin},repo:{active:true,counter:50}});let queue=pending;pending=[];queue.forEach(fn=>fn());return tree;}
function nodes(tree){if(!tree||typeof tree!=='object')return [];if(Array.isArray(tree))return tree.flatMap(nodes);return [tree].concat(tree.children.flatMap(nodes));}
function text(tree){if(typeof tree==='string')return tree;if(!tree||typeof tree!=='object')return '';if(Array.isArray(tree))return tree.map(text).join('');return tree.children.map(text).join('');}
function rowDelete(number){const item=nodes(tree).find(x=>x.props['aria-label']==='Delete build #' + number);assert(item);return item;}
function button(label){let item=nodes(tree).find(x=>x.type==='button'&&text(x)===label);assert(item,'missing '+label);return item;}
async function flush(){await new Promise(resolve=>realTimeout(resolve,0));render();await new Promise(resolve=>realTimeout(resolve,0));render();}
(async()=>{
 render();await flush();
 assert(rowDelete(50).props.disabled);assert(rowDelete(48).props.disabled);assert(!rowDelete(49).props.disabled);
 assert(!nodes(tree).some(x=>x.type==='button'&&text(x).startsWith('Delete selected')));
 let select=nodes(tree).find(x=>x.type==='input'&&x.props.type==='checkbox');select.props.onChange({target:{checked:true}});render();assert(!button('Delete selected (23)').props.disabled);
 button('Next').props.onClick();render();await flush();assert(text(tree).includes('Page 2'));assert(!nodes(tree).some(x=>text(x).startsWith('Delete selected (')));
 rowDelete(1).props.onClick();await flush();assert(text(tree).includes('Single build'));assert(!calls.some(x=>x.options&&x.options.data.commit));
 button('Delete 1 build(s)').props.onClick();await flush();
 let deletion=calls.filter(x=>x.options&&x.options.data.commit).pop();assert.deepStrictEqual(deletion.options.data.numbers,[1]);
 let count=calls.filter(x=>x.options&&x.options.data.commit).length;rowDelete(1).props.onClick();await flush();button('Cancel').props.onClick();render();assert.strictEqual(calls.filter(x=>x.options&&x.options.data.commit).length,count);
 button('Previous').props.onClick();render();await flush();
 button('Clean up history…').props.onClick();render();assert(!text(tree).includes('This page only'));assert(text(tree).includes('All pages'));
 button('Review deletion').props.onClick();await flush();assert(text(tree).includes('All pages in this repository'));assert(text(tree).includes('#1'));button('Delete 23 build(s)').props.onClick();await flush();
 deletion=calls.filter(x=>x.options&&x.options.data.commit).pop();assert(deletion.options.data.non_success);assert.strictEqual(deletion.options.data.numbers,undefined);assert.strictEqual(deletion.options.data.before,undefined);
 button('Clean up history…').props.onClick();render();nodes(tree).find(x=>x.props.value==='before').props.onChange();render();
 let input=nodes(tree).find(x=>x.props.id==='dh-before');input.props.onChange({target:{value:'1.5'}});render();count=calls.length;button('Review deletion').props.onClick();render();assert.strictEqual(calls.length,count);assert(text(tree).includes('positive whole'));
 input.props.onChange({target:{value:'20'}});render();assert(text(tree).includes('Build #20 and newer builds will be kept.'));button('Review deletion').props.onClick();await flush();assert(text(tree).includes('All pages in this repository'));button('Delete 1 build(s)').props.onClick();await flush();deletion=calls.filter(x=>x.options&&x.options.data.commit).pop();assert.strictEqual(deletion.options.data.before,20);
 conflict=true;rowDelete(49).props.onClick();await flush();button('Delete 1 build(s)').props.onClick();await flush();assert(text(tree).includes('Preview and confirm'));assert(!nodes(tree).some(x=>x.type==='button'&&text(x)==='Delete 1 build(s)'));button('Close').props.onClick();render();conflict=false;
 admin=false;render();assert(!nodes(tree).some(x=>x.type==='input'&&x.props.type==='checkbox'));assert(!nodes(tree).some(x=>x.props['aria-label']&&x.props['aria-label'].startsWith('Delete build')));assert(!nodes(tree).some(x=>x.type==='button'&&text(x)==='Clean up history…'));
 console.log('History UI: permissions, page selection, filters, validation, preview, confirmation, cancellation and conflict passed');
})().catch(err=>{console.error(err);process.exitCode=1;});
