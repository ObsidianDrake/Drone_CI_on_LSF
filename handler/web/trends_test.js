const assert=require('assert'), droneTrendsCSS='',A=()=>{};
assert.strictEqual(droneTrendPath([0,1,2,3],[[1],[null],[2],[4]],0,i=>i,v=>v),' M0.00,1.00 M2.00,2.00 L3.00,4.00');
// More available repositories than palette entries must not cause selected color collisions.
const colorRepos=Array.from({length:30},(_,i)=>({id:i+1,slug:'team/repo'+i}));
let colorSeries=colorRepos.slice(0,12);
let colorMap=droneTrendColors(colorRepos,colorSeries,{});
assert.strictEqual(new Set(colorSeries.map(r=>colorMap[r.id])).size,12);
assert.deepStrictEqual(droneTrendColors([...colorRepos].reverse(),[...colorSeries].reverse(),colorMap),colorMap);
const previousColors=Object.fromEntries(colorSeries.map(r=>[r.id,colorMap[r.id]]));
colorSeries=colorRepos.slice(6,18);
colorMap=droneTrendColors(colorRepos,[...colorSeries].reverse(),previousColors);
assert.strictEqual(new Set(colorSeries.map(r=>colorMap[r.id])).size,12);
colorRepos.slice(6,12).forEach(r=>assert.strictEqual(colorMap[r.id],previousColors[r.id]));
let states=[],deps=[],effects=[],pending=[],cursor=0,calls=[],reject=false;
const n={createElement:(type,props,...children)=>({type,props:props||{},children}),
 useRef(v){return this.useState({current:v})[0]},
 useState(v){let i=cursor++;if(!(i in states))states[i]=v;return [states[i],value=>states[i]=typeof value==='function'?value(states[i]):value]},
 useEffect(fn,values){let i=cursor++;if(!deps[i]||values.some((v,k)=>v!==deps[i][k])){deps[i]=values;pending.push(()=>{if(effects[i])effects[i]();effects[i]=fn()})}}
};
const realTimeout=setTimeout;global.setInterval=()=>1;global.clearInterval=()=>{};
function at(url){calls.push(url);if(reject)return Promise.reject(new Error('offline'));const params=new URL(url,'http://test').searchParams;let repos=[{id:1,slug:'team/a'},{id:2,slug:'team/b'}];let selected=params.has('repos')?repos.filter(r=>params.get('repos').split(',').includes(String(r.id))):repos;return Promise.resolve({times:[10,40],series:selected.map(r=>({...r,values:[[1,2,3,4],[2,4,6,8]]})),repositories:repos,last_sample:Math.floor(Date.now()/1000),step:30,lsf_enabled:true});}
let tree;
function render(){cursor=0;tree=DroneTrends({user:{}});let list=pending;pending=[];list.forEach(fn=>fn());}
function nodes(node){if(!node||typeof node!=='object')return [];if(Array.isArray(node))return node.flatMap(nodes);return [node].concat(node.children.flatMap(nodes));}
function text(node){if(typeof node==='string')return node;if(!node||typeof node!=='object')return '';if(Array.isArray(node))return node.map(text).join('');return node.children.map(text).join('');}
function button(label){const found=nodes(tree).find(x=>x.type==='button'&&text(x)===label);assert(found,label);return found;}
async function flush(){for(let i=0;i<4;i++){await new Promise(r=>realTimeout(r,0));render();}}
(async()=>{
 render();await flush();let charts=nodes(tree).filter(x=>x.type===DroneTrendChart);assert.strictEqual(charts.length,2);assert(!charts[0].props.queued&&!charts[1].props.queued);
 assert.strictEqual(charts[0].props.colors,charts[1].props.colors);
 const repoLabels=nodes(tree).filter(x=>x.type==='label'&&x.props.title);
 repoLabels.forEach((label,index)=>assert.strictEqual(nodes(label).find(x=>x.type==='i').props.style.background,charts[0].props.colors[index+1]));
 charts[0].props.setQueued(true);render();charts=nodes(tree).filter(x=>x.type===DroneTrendChart);assert(charts[0].props.queued&&!charts[1].props.queued);charts[1].props.setQueued(true);render();assert(nodes(tree).filter(x=>x.type===DroneTrendChart).every(x=>x.props.queued));
 button('7 days').props.onClick();render();await flush();assert(calls.at(-1).includes('hours=168'));
 button('Clear').props.onClick();render();await flush();assert(calls.at(-1).endsWith('&repos='));assert(nodes(tree).filter(x=>x.type===DroneTrendChart).every(x=>x.props.data.series.length===0));
 button('Select first 12').props.onClick();render();await flush();assert(calls.at(-1).includes('repos=1%2C2'));
 reject=true;button('Refresh').props.onClick();render();await flush();assert(text(tree).includes('Unable to load trends'));assert(nodes(tree).filter(x=>x.type===DroneTrendChart).every(x=>x.props.data===null));
 console.log('Trend UI: independent queue switches, time range, selection, gaps and error clearing passed');
})().catch(err=>{console.error(err);process.exitCode=1;});
