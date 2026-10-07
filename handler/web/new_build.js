// Uses the pinned bundle's React, API client and existing form components.
function droneIsCreatedBuild(build) {
  return !!build && !Array.isArray(build) &&
    Number.isSafeInteger(build.id) && build.id > 0 &&
    Number.isSafeInteger(build.number) && build.number > 0;
}

function droneNewBuildRequest(namespace, name, showSuccess, refresh) {
  return function (values) {
    var query = new URLSearchParams();
    if (values.target) query.set('branch', values.target);
    if (values.commit) query.set('commit', values.commit);
    (values.parameters || []).forEach(function (parameter) { query.set(parameter.key, parameter.value); });
    var suffix = query.toString();
    return at('/api/repos/' + encodeURIComponent(namespace) + '/' + encodeURIComponent(name) + '/builds' + (suffix ? '?' + suffix : ''), {method:'POST'}).then(function (build) {
      if (!droneIsCreatedBuild(build)) return null;
      showSuccess(n.createElement('div', null,
        n.createElement('div', null, 'Build #' + build.number + ' was created'),
        n.createElement('div', {style:{fontSize:12, marginTop:4, overflowWrap:'anywhere'}},
          'Branch: ' + (build.branch || query.get('branch') || 'Default branch') + ' · Event: custom')));
      // A cache refresh failure must not invite resubmission of a created build.
      Promise.resolve().then(function () { return refresh(build); }).catch(function (error) { console.warn(error); });
      return build;
    });
  };
}

function droneNewBuildForm(props) {
  var h = n.createElement;
  var valuesState = n.useState({target:props.target || '', parameters:props.parameters || []});
  var values = valuesState[0], setValues = valuesState[1];
  var parameterState = n.useState({key:'', value:''}), parameter = parameterState[0], setParameter = parameterState[1];
  var busyState = n.useState(false), busy = busyState[0], setBusy = busyState[1];
  var feedbackState = n.useState(null), feedback = feedbackState[0], setFeedback = feedbackState[1];
  var pending = n.useRef(false), mounted = n.useRef(true);
  n.useEffect(function () {
    mounted.current = true;
    return function () { mounted.current = false; props.onBusyChange(false); };
  }, []);
  function finish() {
    pending.current = false;
    if (mounted.current) { setBusy(false); props.onBusyChange(false); }
  }
  function submit(event) {
    event.preventDefault();
    if (pending.current) return;
    pending.current = true; setBusy(true); props.onBusyChange(true); setFeedback(null);
    // Match the submitted query, including the existing parameter override behavior.
    var branch = values.target || props.defaultBranch || 'Default branch';
    values.parameters.forEach(function (item) { if (item.key === 'branch') branch = item.value || props.defaultBranch || 'Default branch'; });
    return Promise.resolve().then(function () { return props.handleSubmit(values); }).then(function (build) {
      if (!mounted.current) return;
      finish();
      if (droneIsCreatedBuild(build)) props.handleCancel();
      else setFeedback({kind:'warning', branch:branch});
    }, function () {
      if (!mounted.current) return;
      finish(); setFeedback({kind:'error', branch:branch});
    });
  }
  function changeParameter(key) {
    return function (event) { setParameter(Object.assign({}, parameter, {[key]:event.target.value.trim()})); };
  }
  var noteStyle = {fontSize:13, lineHeight:'20px', color:'var(--color-summary)', margin:'12px 0'};
  return h(ii, {className:mb('new-build-form'), onSubmit:submit, 'aria-busy':busy},
    h('fieldset', {disabled:busy, style:{border:0, padding:0, margin:0, minWidth:0}},
      h(li, {className:mb('new-build-form-column')},
        h(oi.Input, {label:'Branch', placeholder:props.defaultBranch || '<default branch name>', value:values.target, name:'branch',
          onChange:function (event) { setValues(Object.assign({}, values, {target:event.target.value.trim()})); }}),
        h('p', {style:noteStyle}, 'Manual builds use the custom event.')),
      h(li, {title:'Parameters', className:mb('new-build-form-column')},
        values.parameters.length ? h('div', {className:mb('new-build-form-parameters-list')}, values.parameters.map(function (item, index) {
          return h('div', {key:index, className:mb('new-build-form-parameters')},
            h(oi.Input, {value:item.key, name:item.key, readOnly:true}),
            h(oi.Input, {value:item.value, name:item.value, readOnly:true}),
            h(Ye, {theme:'plain', type:'button', onClick:function () {
              setValues(Object.assign({}, values, {parameters:values.parameters.filter(function (_, i) { return i !== index; })}));
            }}, 'Remove'));
        })) : null,
        h('div', {className:mb('new-build-form-parameters-fields')},
          h(oi.Input, {name:'newKey', placeholder:'key', value:parameter.key, onChange:changeParameter('key')}),
          h(oi.Input, {name:'newVal', placeholder:'value', value:parameter.value, onChange:changeParameter('value')}),
          h(Ye, {theme:'plain', type:'button', onClick:function () {
            if (!parameter.key || !parameter.value) return;
            setValues(Object.assign({}, values, {parameters:values.parameters.concat([parameter])}));
            setParameter({key:'', value:''});
          }}, '+ Add')))),
    feedback && h('div', {role:'alert', style:{padding:16, margin:'16px 0', borderRadius:6, border:'1px solid ' + (feedback.kind === 'error' ? '#dc3545' : '#b7791f'), background:feedback.kind === 'error' ? 'rgba(220,53,69,.08)' : 'rgba(183,121,31,.08)', fontSize:13, lineHeight:'20px', overflowWrap:'anywhere'}},
      h('strong', null, feedback.kind === 'error' ? 'Unable to create build' : 'No build was created'),
      h('p', {style:{margin:'8px 0'}}, feedback.kind === 'error' ? 'The server returned an error. Please try again.' : 'The request completed, but no build was created.'),
      h('div', null, 'Branch: ', feedback.branch), h('div', null, 'Event: custom'),
      feedback.kind === 'warning' && h('p', {style:{margin:'8px 0 0'}}, "Check the pipeline trigger conditions in this branch's .drone.yml. Make sure they allow the custom event and match the selected branch and ref.")),
    h(li, {className:mb('new-build-form-controls')},
      h(Ye, {theme:'primary', type:'submit', disabled:busy}, busy ? 'Creating...' : 'Create Build'),
      h(Ye, {theme:'primary', type:'button', disabled:busy, onClick:props.handleCancel}, 'Cancel')));
}
