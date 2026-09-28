// Replaces only the pinned Builds component. Uses the existing React, router,
// authenticated API client, summary, build cards and button components.
function qu(props) {
  var h = n.createElement, route = b.j(), history = b.g();
  var namespace = route.params.namespace, name = route.params.name;
  var api = '/api/repos/' + encodeURIComponent(namespace) + '/' + encodeURIComponent(name) + '/builds';
  var pageState = n.useState(1), page = pageState[0], setPage = pageState[1];
  var dataState = n.useState([]), data = dataState[0], setData = dataState[1];
  var selectedState = n.useState([]), selected = selectedState[0], setSelected = selectedState[1];
  var busyState = n.useState(false), busy = busyState[0], setBusy = busyState[1];
  var loadingState = n.useState(true), loading = loadingState[0], setLoading = loadingState[1];
  var messageState = n.useState(''), message = messageState[0], setMessage = messageState[1];
  var beforeState = n.useState(''), before = beforeState[0], setBefore = beforeState[1];
  var refreshState = n.useState(0), refresh = refreshState[0], setRefresh = refreshState[1];
  var dialogState = n.useState(null), dialog = dialogState[0], setDialog = dialogState[1];
  var modeState = n.useState('non-success'), mode = modeState[0], setMode = modeState[1];
  var errorState = n.useState(''), dialogError = errorState[0], setDialogError = errorState[1];
  var dialogRef = n.useRef(null);
  var admin = !!(props.user && props.user.admin), limit = 25;
  A(namespace + '/' + name);
  function terminal(build) { return ['success','failure','error','killed','skipped'].indexOf(build.status) >= 0; }
  n.useEffect(function () {
    setPage(1); setSelected([]); setData([]); setMessage(''); setDialog(null);
  }, [api]);
  n.useEffect(function () {
    if (!props.repo.active) history.replace('/' + namespace + '/' + name + '/settings');
  }, [props.repo.active, namespace, name, history]);
  n.useEffect(function () {
    var active = true;
    setLoading(true); setSelected([]);
    function load() {
      at(api + '?page=' + page + '&per_page=' + limit).then(function (rows) {
        if (!active) return;
        rows = rows || [];
        if (!rows.length && page > 1) { setPage(page - 1); return; }
        setData(rows); setLoading(false);
        setSelected(function (old) { return old.filter(function (number) { return rows.some(function (row) { return row.number === number && terminal(row); }); }); });
      }).catch(function (err) { if (active) { setMessage('Unable to load builds: ' + err.message); setLoading(false); setData([]); } });
    }
    load();
    var timer = setInterval(load, 10000);
    return function () { active = false; clearInterval(timer); };
  }, [api, page, refresh]);
  var eligible = data.filter(terminal).map(function (row) { return row.number; });
  n.useEffect(function () {
    if (!dialog) return;
    var previous = document.activeElement;
    if (dialogRef.current) {
      var focus = dialogRef.current.querySelector('[data-initial-focus]');
      if (focus) focus.focus();
    }
    var overflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return function () { document.body.style.overflow = overflow; if (previous && previous.isConnected) previous.focus(); };
  }, [dialog && dialog.kind]);
  function closeDialog() { if (!busy) { setDialog(null); setDialogError(''); } }
  function preview(filter, label, scope) {
    if (busy) return;
    setBusy(true); setDialogError(''); setMessage('');
    at(api + '/history/delete', {method:'POST', data:filter}).then(function (result) {
      if (!result.numbers.length) {
        setDialog({kind:'empty'});
        return;
      }
      setDialog({kind:'confirm', filter:filter, preview:result, label:label, scope:scope});
    }).catch(function (err) {
      if (dialog) setDialogError('Unable to preview: ' + err.message);
      else setMessage('Unable to preview: ' + err.message);
    }).then(function () { setBusy(false); });
  }
  function commit() {
    if (busy || !dialog || dialog.kind !== 'confirm') return;
    setBusy(true); setDialogError('');
    at(api + '/history/delete', {method:'POST', data:Object.assign({}, dialog.filter, {commit:true, token:dialog.preview.token})}).then(function (result) {
      setMessage('Deleted ' + result.numbers.length + ' build(s) and their logs.' + (result.cleanup_failures ? ' Warning: ' + result.cleanup_failures + ' external log objects could not be removed. Contact your administrator.' : ''));
      setSelected([]); setDialog(null); setRefresh(function (x) {return x + 1;});
    }).catch(function (err) {
      // A stale preview must never leave a reusable destructive confirmation.
      setDialog({kind:'changed'}); setDialogError(err.message);
      setSelected([]); setRefresh(function (x) {return x + 1;});
    }).then(function () { setBusy(false); });
  }
  function button(label, onClick, disabled, variant, extra) {
    return h('button', Object.assign({type:'button', onClick:onClick, disabled:busy || disabled,
      className:'dh-button dh-button--' + (variant || 'default')}, extra || {}), label);
  }
  function trash() {
    return h('svg', {width:16,height:16,viewBox:'0 0 24 24',fill:'none',stroke:'currentColor',strokeWidth:1.6,'aria-hidden':true},
      h('path', {d:'M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7'}));
  }
  function showCleanup() { setMode('non-success'); setDialogError(''); setDialog({kind:'choose'}); }
  function reviewCleanup() {
    if (mode === 'non-success') {
      preview({non_success:true}, 'Non-success builds', 'All pages in this repository');
    } else {
      if (!/^[0-9]+$/.test(before) || !Number.isSafeInteger(Number(before)) || Number(before) < 1) {
        setDialogError('Enter a positive whole build number, for example 20.'); return;
      }
      preview({before:Number(before)}, 'Builds before #' + before, 'All pages in this repository');
    }
  }
  function dialogKeys(event) {
    if (event.key === 'Escape') { event.preventDefault(); closeDialog(); }
    if (event.key !== 'Tab') return;
    var items = event.currentTarget.querySelectorAll('button:not(:disabled), input:not(:disabled), [tabindex="0"]');
    if (!items.length) { event.preventDefault(); return; }
    var first = items[0], last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) {event.preventDefault(); last.focus();}
    else if (!event.shiftKey && document.activeElement === last) {event.preventDefault(); first.focus();}
  }
  var modal = null;
  if (admin && dialog) {
    var choosing = dialog.kind === 'choose', confirming = dialog.kind === 'confirm';
    var title = choosing ? 'Clean up build history' : confirming ? 'Delete ' + dialog.preview.numbers.length + ' build(s)?' : dialog.kind === 'empty' ? 'No builds to delete' : 'Review the selection again';
    modal = h('div', {className:'dh-overlay'}, h('div', {className:'dh-dialog',role:'dialog','aria-modal':true,'aria-labelledby':'dh-dialog-title','aria-describedby':'dh-dialog-description',ref:dialogRef,onKeyDown:dialogKeys},
      h('div', {className:'dh-dialog-header'}, h('span', {className:'dh-eyebrow'}, namespace + '/' + name), h('h2', {id:'dh-dialog-title'}, title),
        h('p', {id:'dh-dialog-description',className:'dh-muted'}, choosing ? 'Choose which completed builds to remove. Review the exact count before deleting.' : confirming ? 'Build records, steps and execution logs will be permanently removed.' : dialog.kind === 'empty' ? 'Nothing has been deleted by this request.' : 'Check the current build history and preview the selection again.')),
      h('div', {className:'dh-dialog-body'}, choosing ? h(n.Fragment, null,
        h('label', {className:'dh-option' + (mode === 'non-success' ? ' is-active' : '')},
          h('input', {type:'radio',name:'cleanup-mode',value:'non-success',checked:mode === 'non-success',disabled:busy,'data-initial-focus':true,onChange:function () {setMode('non-success');setDialogError('');}}),
          h('span', null, h('strong', null, 'Non-success builds'), h('span', {className:'dh-scope dh-scope--all'}, 'All pages'), h('small', null, 'Failure, error, canceled or skipped. Successful builds are kept.'))),
        h('label', {className:'dh-option' + (mode === 'before' ? ' is-active' : '')},
          h('input', {type:'radio',name:'cleanup-mode',value:'before',checked:mode === 'before',disabled:busy,onChange:function () {setMode('before');setDialogError('');}}),
          h('span', null, h('strong', null, 'Older builds'), h('span', {className:'dh-scope dh-scope--all'}, 'All pages'), h('small', null, 'Remove completed builds below a build number, including successful ones.'))),
        mode === 'before' ? h('div', {className:'dh-threshold'}, h('label', {htmlFor:'dh-before'}, 'Delete builds before'),
          h('div', {className:'dh-number'}, h('span', null, '#'), h('input', {id:'dh-before',type:'number',min:1,step:1,placeholder:'e.g. 20',value:before,disabled:busy,'aria-describedby':'dh-before-help','aria-invalid':!!dialogError,onChange:function (e) {setBefore(e.target.value);setDialogError('');}})),
          h('p', {id:'dh-before-help',className:'dh-muted'}, /^[0-9]+$/.test(before) && Number(before)>0 ? 'Build #' + before + ' and newer builds will be kept.' : 'For example, 20 removes #1–#19. Build #20 is kept.')) :
          h('p', {className:'dh-hint'}, 'Checks all pages in this repository. The next screen shows the exact number of builds to delete.'),
        h('p', {className:'dh-protection'}, 'Queued, running and approval-waiting builds are always kept.')) : confirming ? h(n.Fragment, null,
          h('dl', {className:'dh-facts'}, h('div', null,h('dt',null,'Selection'),h('dd',null,dialog.label)),h('div',null,h('dt',null,'Scope'),h('dd',null,dialog.scope)),h('div',null,h('dt',null,'To delete'),h('dd',{className:'dh-count'},dialog.preview.numbers.length + ' completed build(s)'))),
          h('div', {className:'dh-build-numbers'}, dialog.preview.numbers.slice(0,25).map(function (number) {return h('span',{key:number},'#' + number);}),dialog.preview.numbers.length>25 ? h('span',null,'+' + (dialog.preview.numbers.length-25) + ' more') : null),
          h('p', {className:'dh-warning'}, 'This cannot be undone. Source code and Git commits are not affected.')) :
          h('p',{className:'dh-muted'},dialog.kind==='empty' ? 'No completed builds match this selection. Builds still finishing are kept.' : 'The request could not be completed. Refresh and review the current build history before trying again.'),
        dialogError ? h('p', {className:'dh-warning',role:'alert'},dialogError) : null),
      h('div', {className:'dh-dialog-footer'}, button(choosing || confirming ? 'Cancel' : 'Close',closeDialog,false,'default',{'data-initial-focus':!choosing}),
        choosing ? button(busy ? 'Checking…' : 'Review deletion',reviewCleanup,loading,'primary') :
        confirming ? button(busy ? 'Deleting…' : 'Delete ' + dialog.preview.numbers.length + ' build(s)',commit,false,'danger') : null)));
  }
  return h('section', {className:Gu('wrapper') + ' dh-history','aria-busy':busy || loading},
    h('style',null,droneHistoryCSS),
    data.length ? h(n.Fragment,null,h('h2',{className:Gu('section-title')},'Summary (this page)'),h(Zu,{data:data,totalBuildsCounter:data.length,className:Gu('summary')})) : null,
    h('div',{className:'dh-heading'},h('div',null,h('h2',null,'Build history'),h('p',{className:'dh-muted'},'Page ' + page + ' · ' + data.length + ' builds')),
      h('div',{className:'dh-actions'},button('Refresh',function(){setRefresh(function(x){return x+1;});},loading,'quiet'),admin ? button('Clean up history…',showCleanup,loading) : null)),
    admin ? h('div',{className:'dh-selection'},h('div',{className:'dh-select-label'},h('label',null,h('input',{type:'checkbox','aria-label':'Select completed builds on this page',disabled:busy || loading || !eligible.length,checked:eligible.length>0 && eligible.every(function(x){return selected.indexOf(x)>=0;}),onChange:function(e){setSelected(e.target.checked ? eligible : []);}}),' Select this page'),h('span',{className:'dh-muted'},selected.length ? selected.length + ' selected' : 'Completed builds only')),
      selected.length ? h('div',{className:'dh-actions'},button('Clear',function(){setSelected([]);},false,'quiet'),button('Delete selected (' + selected.length + ')',function(){preview({numbers:selected},'Selected builds','This page · Page ' + page);},loading,'danger-outline')) : h('span',{className:'dh-selection-help'},'Select builds to delete their records and logs.')) : null,
    message ? h('p',{role:'status',className:'dh-notice'},message) : null,
    loading ? h('p',null,'Loading builds…') : !data.length ? h('p',{className:'dh-empty'},'Your Build List is Empty.') :
    h('div',{className:'dh-rows'},data.map(function(row){return h('div',{key:row.number,className:'dh-row' + (selected.indexOf(row.number)>=0 ? ' is-selected' : '')},
      admin ? h('input',{type:'checkbox','aria-label':'Select build #' + row.number,disabled:busy || !terminal(row),checked:selected.indexOf(row.number)>=0,onChange:function(e){setSelected(e.target.checked ? selected.concat([row.number]) : selected.filter(function(x){return x!==row.number;}));}}) : null,
      h('div',{className:'dh-build'},h(Mu,{data:[row],url:route.url})),
      admin ? button(trash(),function(){preview({numbers:[row.number]},'Build #' + row.number,'Single build');},!terminal(row),'icon',{'aria-label':'Delete build #' + row.number,title:terminal(row) ? 'Delete build #' + row.number + ' and its logs' : 'This build has not finished'}) : null);
    })),
    h('nav',{'aria-label':'Build history pages',className:'dh-pagination'},h('span',{className:'dh-muted'},'25 builds per page'),h('div',{className:'dh-actions'},button('Previous',function(){setPage(page-1);},loading || page===1),h('span',null,'Page ' + page),button('Next',function(){setPage(page+1);},loading || data.length<limit))),modal);
}
