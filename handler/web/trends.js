function DroneTrendIcon(){return n.createElement('svg',{width:24,height:24,viewBox:'0 0 24 24',fill:'none',stroke:'currentColor',strokeWidth:1.7,'aria-hidden':true},n.createElement('path',{d:'M3 3v18h18M6 15l4-5 4 3 6-8'}));}
function droneTrendColor(repo){var hash=0;for(var i=0;i<repo.slug.length;i++)hash=(hash*31+repo.slug.charCodeAt(i))>>>0;hash=Math.imul(hash^(hash>>>16),0x7feb352d);hash=Math.imul(hash^(hash>>>15),0x846ca68b);hash=(hash^(hash>>>16))>>>0;return 'hsl('+(hash%360)+', 67%, 47%)';}
function droneTrendPath(times,values,column,x,y){var d='',connected=false;for(var i=0;i<times.length;i++){var value=values[i]&&values[i][column];if(value===null||value===undefined){connected=false;continue;}d+=(connected?' L':' M')+x(i).toFixed(2)+','+y(value).toFixed(2);connected=true;}return d;}
function DroneTrendChart(props){
 var h=n.createElement, host=n.useRef(null), widthState=n.useState(900), width=widthState[0];
 n.useEffect(function(){if(!host.current||typeof ResizeObserver==='undefined')return;var observer=new ResizeObserver(function(entries){widthState[1](Math.max(360,entries[0].contentRect.width));});observer.observe(host.current);return function(){observer.disconnect();};},[]);
 var times=props.data?props.data.times:[],series=props.data?props.data.series:[],column=props.column+(props.queued?1:0);
 var height=258,left=44,right=18,top=18,bottom=36,maximum=0,points=0;
 series.forEach(function(repo){repo.values.forEach(function(v){if(v[column]!==null){points++;maximum=Math.max(maximum,v[column]);}});});
 var unit=Math.max(1,Math.pow(10,Math.floor(Math.log10(Math.max(1,maximum/4)))));while(unit*4<maximum)unit*=2;
 var yMax=unit*4,x=function(i){return left+i/Math.max(1,times.length-1)*(width-left-right);},y=function(v){return top+(1-v/yMax)*(height-top-bottom);};
 var drawing=[];
 for(var i=0;i<=4;i++){drawing.push(h('g',{key:'grid'+i},h('line',{x1:left,x2:width-right,y1:y(i*unit),y2:y(i*unit),className:'dt-grid'}),h('text',{x:left-10,y:y(i*unit)+4,textAnchor:'end',className:'dt-axis'},String(i*unit))));}
 var ticks=width<600?2:4;
 if(times.length)for(var k=0;k<=ticks;k++){var index=Math.round((times.length-1)*k/ticks);drawing.push(h('text',{key:'time'+k,x:x(index),y:height-10,textAnchor:k===0?'start':k===ticks?'end':'middle',className:'dt-axis'},new Date(times[index]*1000).toLocaleString(undefined,props.hours>24?{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'}:{hour:'2-digit',minute:'2-digit'})));}
 series.forEach(function(repo){var color=droneTrendColor(repo);drawing.push(h('path',{key:repo.id,d:droneTrendPath(times,repo.values,column,x,y),stroke:color,fill:'none',strokeWidth:2,strokeLinejoin:'round',vectorEffect:'non-scaling-stroke'}));
 // Preserve visibility of isolated valid samples without bridging gaps.
 repo.values.forEach(function(v,index){if(v[column]!==null && (index===0||repo.values[index-1][column]===null) && (index===times.length-1||repo.values[index+1][column]===null))drawing.push(h('circle',{key:repo.id+'-'+index,cx:x(index),cy:y(v[column]),r:2.5,fill:color}));});
 });
 var hovered=props.hover!==null&&props.hover<times.length?props.hover:null;
 if(hovered!==null)drawing.push(h('line',{key:'cursor',x1:x(hovered),x2:x(hovered),y1:top,y2:height-bottom,className:'dt-cursor'}));
 function locate(e){if(!times.length)return;var rect=e.currentTarget.getBoundingClientRect();props.onHover(Math.max(0,Math.min(times.length-1,Math.round(((e.clientX-rect.left)*width/rect.width-left)/(width-left-right)*(times.length-1)))));}
 var noData=!props.data?'Loading samples…':!series.length?'Select repositories to compare.':!points?'No samples available in this time range.':null;
 return h('article',{className:'dt-chart',onMouseLeave:function(){props.onHover(null);},onBlur:function(e){if(!e.currentTarget.contains(e.relatedTarget))props.onHover(null);}},h('div',{className:'dt-chart-heading'},h('div',null,h('h2',null,props.title),h('p',null,props.queued?props.column===0?'Running + pending builds':'RUN + PEND · Drone-submitted jobs':props.column===0?'Running builds only':'RUN · Drone-submitted jobs')),
 h('label',{className:'dt-switch'},h('input',{type:'checkbox',checked:props.queued,onChange:function(e){props.setQueued(e.target.checked);}}),' Include queued')),
 h('div',{className:'dt-plot',ref:host},h('svg',{viewBox:'0 0 '+width+' '+height,role:'img',tabIndex:0,'aria-label':props.title+'. Time on X axis, count on Y axis. Arrow keys explore samples.',onMouseMove:locate,onKeyDown:function(e){if(e.key==='ArrowLeft'||e.key==='ArrowRight'){e.preventDefault();props.onHover(Math.max(0,Math.min(times.length-1,(hovered===null?times.length-1:hovered)+(e.key==='ArrowLeft'?-1:1))));}}},drawing),noData?h('div',{className:'dt-plot-empty'},noData):null),
 h('div',{className:'dt-tooltip','aria-live':'polite',tabIndex:0,'aria-label':'Sample details'},hovered!==null?[h('strong',{key:'time'},new Date(times[hovered]*1000).toLocaleString()),series.map(function(repo){return h('span',{key:repo.id},h('i',{style:{background:droneTrendColor(repo)}}),repo.slug,h('b',null,repo.values[hovered][column]===null?'No data':repo.values[hovered][column]));})]:h('span',{className:'dt-tooltip-hint'},'Hover over the chart or focus it and use arrow keys to inspect a sample.')),
 h('div',{className:'dt-chart-note'},props.data&&props.data.step>30?'Each point is the peak sampled count in a '+props.data.step/60+'-minute interval.':'One sample every 30 seconds.', ' Gaps mean unavailable data.'));
}
function DroneTrends(props){
 var h=n.createElement;
 var range=n.useState(6),hours=range[0],setHours=range[1];
 var selection=n.useState(null),selected=selection[0],setSelected=selection[1];
 var result=n.useState(null),data=result[0],setData=result[1];
 var failure=n.useState(''),error=failure[0],setError=failure[1];
 var searchState=n.useState(''),search=searchState[0];
 var builds=n.useState(false),jobs=n.useState(false),hoverState=n.useState(null);
 var refresh=n.useState(0),busyState=n.useState(false),busy=busyState[0];
 var key=selected===null?'auto':selected.join(',');
 A('Workload trends');
 n.useEffect(function(){var active=true,inflight=false;
 function load(){if(inflight)return;inflight=true;busyState[1](true);
 at('/api/monitor/trends?hours='+hours+(selected===null?'':'&repos='+encodeURIComponent(key))).then(function(next){if(!active)return;setData(next);setError('');if(selected===null)setSelected(next.series.map(function(repo){return repo.id;}));else{var ids=selected.filter(function(id){return next.repositories.some(function(repo){return repo.id===id;});});if(ids.length!==selected.length)setSelected(ids);}}).catch(function(err){if(active){setData(null);setError('Unable to load trends: '+err.message);}}).then(function(){inflight=false;if(active)busyState[1](false);});}
 load();var timer=setInterval(load,30000);return function(){active=false;clearInterval(timer);};},[hours,key,refresh[0]]);
 var repos=data?data.repositories:[],filtered=repos.filter(function(repo){return repo.slug.toLowerCase().indexOf(search.toLowerCase())>=0;});
 var chosen=selected||[],stale=data&&(!data.last_sample||Date.now()/1000-data.last_sample>90);
 function toggle(id){setSelected(chosen.indexOf(id)>=0?chosen.filter(function(x){return x!==id;}):chosen.length<12?chosen.concat([id]):chosen);hoverState[1](null);}
 return h('div',{className:'dt-page'},h('style',null,droneTrendsCSS),
 h('div',{className:'dt-top'},h('div',null,h('span',{className:'dt-eyebrow'},'WORKLOAD MONITOR'),h('h1',null,'Workload trends'),h('p',null,'Compare build activity and LSF demand across your repositories.')),
 h('div',{className:'dt-status'},h('i',{className:error||stale?'is-stale':''}),error?'Connection unavailable':stale?'Waiting for samples':data?'Sampling every 30s':'Loading…')),
 h('div',{className:'dt-controls'},h('div',{className:'dt-ranges',role:'group','aria-label':'Time range'},[[1,'1 hour'],[6,'6 hours'],[24,'24 hours'],[72,'3 days'],[168,'7 days']].map(function(item){return h('button',{key:item[0],type:'button','aria-pressed':hours===item[0],className:hours===item[0]?'is-active':'',onClick:function(){setHours(item[0]);hoverState[1](null);}},item[1]);})),h('div',{className:'dt-update'},h('span',null,data&&data.last_sample?'Last sample '+new Date(data.last_sample*1000).toLocaleTimeString():'History starts when sampling is enabled'),h('button',{type:'button',disabled:busy,onClick:function(){refresh[1](refresh[0]+1);}},busy?'Updating…':'Refresh'))),
 error?h('p',{role:'alert',className:'dt-notice'},error):null,
 data&&data.permission_errors?h('p',{role:'status',className:'dt-notice'},'Some repository permissions could not be verified. Those repositories are not shown.'):null,
 data&&!data.lsf_enabled?h('p',{role:'status',className:'dt-notice'},'LSF sampling requires the local LSF runner. Build history is still available.'):null,
 h('div',{className:'dt-layout'},h('aside',{className:'dt-repositories'},h('div',{className:'dt-repo-title'},h('h2',null,'Repositories'),h('span',null,chosen.length+'/12')),
 h('p',null,'Only repositories you can access.'),h('input',{type:'search',placeholder:'Find a repository…','aria-label':'Find a repository',value:search,onChange:function(e){searchState[1](e.target.value);}}),
 h('div',{className:'dt-repo-actions'},h('button',{type:'button',onClick:function(){setSelected(filtered.slice(0,12).map(function(repo){return repo.id;}));}},'Select first 12'),h('button',{type:'button',onClick:function(){setSelected([]);}},'Clear')),
 h('div',{className:'dt-repo-list'},filtered.map(function(repo){return h('label',{key:repo.id,title:repo.slug},h('input',{type:'checkbox',checked:chosen.indexOf(repo.id)>=0,disabled:chosen.length>=12&&chosen.indexOf(repo.id)<0,onChange:function(){toggle(repo.id);}}),h('i',{style:{background:droneTrendColor(repo)}}),h('span',null,repo.slug));}),!filtered.length?h('p',null,'No matching repositories.'):null),
 h('p',{className:'dt-retention'},'7-day history',h('br'),'Counts are sampled, not a complete job audit.')),
 h('div',{className:'dt-charts'},h(DroneTrendChart,{title:'Builds processing',data:data,hours:hours,column:0,queued:builds[0],setQueued:builds[1],hover:hoverState[0],onHover:hoverState[1]}),h(DroneTrendChart,{title:'LSF jobs processing',data:data,hours:hours,column:2,queued:jobs[0],setQueued:jobs[1],hover:hoverState[0],onHover:hoverState[1]}))),
 h('p',{className:'dt-footnote'},'Same repository, same color in both charts. LSF counts represent bsub jobs, not CPU cores. Only jobs submitted by Drone after tracking was enabled are included.'));
}
