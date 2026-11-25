import React, { useEffect, useState } from 'react';

const departments = 'AMAZONAS|ANCASH|APURIMAC|AREQUIPA|AYACUCHO|CAJAMARCA|CALLAO|CUSCO|HUANCAVELICA|HUANUCO|ICA|JUNIN|LA LIBERTAD|LAMBAYEQUE|LIMA|LORETO|MADRE DE DIOS|MOQUEGUA|PASCO|PIURA|PUNO|SAN MARTIN|TACNA|TUMBES|UCAYALI'.split('|');
const number = (x, digits = 0) => typeof x === 'number' ? new Intl.NumberFormat('es-PE', { maximumFractionDigits: digits }).format(x) : '—';
const percent = x => typeof x === 'number' ? number(x * 100, 2) + '%' : '—';
const clock = x => x ? new Date(x).toLocaleTimeString('es-PE') : '—';
const friendly = x => x?.replaceAll('_', ' ') || '—';
const list = x => x || [];
const active = x => ['en_cola', 'distribuyendo', 'entrenando', 'evaluando'].includes(x?.estado);
const month = value => value ? String(value).slice(4) + '/' + String(value).slice(0, 4) : '—';

function useData(api, path, revision = 0) {
  const [state, setState] = useState({});
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    api(path, {signal: controller.signal})
      .then(data => setState({path, data}))
      .catch(error => {
        if (!controller.signal.aborted) setState({path, error: error.message});
      });
    return () => controller.abort();
  }, [api, path, revision, retry]);
  return {...(state.path === path ? state : {}), reload: () => setRetry(n => n + 1)};
}

function PageTitle({ title, children }) {
  return <header className="page-title"><h1>{title}</h1><p>{children}</p></header>;
}
function Notice({ state, children }) {
  if (state.error) return <div className="notice error" role="alert">{state.error} <button onClick={state.reload}>Reintentar</button></div>;
  if (state.data === undefined) return <p className="notice" role="status">Cargando datos…</p>;
  return children;
}
function Stat({ title, value, detail }) {
  return <div className="stat"><span>{title}</span><strong>{value}</strong>{detail && <small>{detail}</small>}</div>;
}
function Table({ columns, rows, empty = 'Aún no hay registros.' }) {
  if (!rows.length) return <p className="notice">{empty}</p>;
  return <div className="table-wrap"><table><thead><tr>{columns.map(column => <th scope="col" key={column}>{column}</th>)}</tr></thead>
    <tbody>{rows.map(([id, ...cells]) => <tr key={id}>{cells.map((value, i) => <td key={columns[i]}>{value}</td>)}</tr>)}</tbody>
  </table></div>;
}
function Department({ name, value, onChange, all }) {
  return <label>Departamento<select name={name} value={value} onChange={onChange} defaultValue={value === undefined ? 'LIMA' : undefined}>
    {all && <option value="">Todo el Perú</option>}
    {departments.map(dept => <option key={dept}>{dept}</option>)}
  </select></label>;
}
function Bars({ rows, value, unit, max }) {
  return <ul className="bars">{rows.map(row => <li key={row.departamento + row.provincia}>
    <div><strong>{row.provincia}</strong> <small>{row.departamento}</small><span>{number(row[value], 2)} {unit}</span></div>
    <progress aria-label={row.provincia + ' · ' + unit} value={row[value]} max={max}/>
  </li>)}</ul>;
}

export function Dashboard({ api, live }) {
  const [dept, setDept] = useState('');
  const [sort, setSort] = useState('graves_esperados');
  const dataset = useData(api, '/api/dataset', live.revision);
  const impact = useData(api, '/api/impacto?' + new URLSearchParams({top: sort === 'graves_esperados' ? '15' : '300', departamento: dept}), live.revision);
  const data = dataset.data;
  const rows = list(impact.data?.provincias);
  const ranked = [...rows].sort((a, b) => b[sort] - a[sort]).slice(0, 15);
  const unit = sort === 'graves_esperados' ? 'estimados' : 'por mil';
  return <>
    <PageTitle title="Panorama epidemiológico">Compara la carga histórica estimada de COVID-19 entre provincias para orientar la planificación sanitaria.</PageTitle>
    <Notice state={dataset}>
      {data?.fuente_datos && <p className="footnote dataset-source">Fuente de esta ejecución: {data.fuente_datos}.</p>}
      <div className="stats">
        <Stat title="Positivos procesados" value={number(data?.positivos)}/>
        <Stat title="Defunciones procesadas" value={number(data?.fallecidos)}/>
        <Stat title="Provincias en el dataset" value={number(data?.provincias)}/>
        <Stat title="Modelo activo" value={impact.data ? 'v' + impact.data.modelo_version : '—'} detail={'Ventana desde ' + month(impact.data?.casos_desde)}/>
      </div>
    </Notice>
    <section className="panel">
      <div className="panel-head"><div><h2>Impacto por provincia</h2><p>¿Dónde se concentraría la mayor carga entre los positivos de la ventana histórica?</p></div>
      <div className="filters"><Department all value={dept} onChange={e => setDept(e.target.value)}/>
        <label>Ordenar por<select value={sort} onChange={e => setSort(e.target.value)}><option value="graves_esperados">Volumen estimado</option><option value="tasa_por_1000">Tasa por mil positivos</option></select></label></div></div>
      <Notice state={impact}>
        {ranked.length > 0 && <Bars rows={ranked.slice(0, 6)} value={sort} unit={unit} max={Math.max(1, ...ranked.map(row => row[sort]))}/>}
        <Table columns={['Provincia', 'Departamento', 'Positivos', 'Carga estimada', 'Tasa por mil']} empty="Sin provincias para este filtro."
          rows={ranked.map(r => [r.departamento + r.provincia, r.provincia, r.departamento, number(r.positivos), number(r.graves_esperados, 2), number(r.tasa_por_1000, 2)])}/>
      </Notice>
      <p className="footnote">La carga suma estimaciones de letalidad sobre positivos registrados. Se utiliza como indicador exploratorio de severidad y depende del volumen, el periodo y la cobertura de los datos.</p>
    </section>
  </>;
}

export function Prediction({ api }) {
  const [result, setResult] = useState();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  async function submit(event) {
    event.preventDefault();
    const fields = Object.fromEntries(new FormData(event.currentTarget));
    setError('');
    setResult(undefined);
    setBusy(true);
    try { setResult(await api('/api/predicciones', {body: {...fields, edad: Number(fields.edad)}})); }
    catch (e) { setError(e.message); }
    finally { setBusy(false); }
  }
  return <>
    <PageTitle title="Simulación de casos">Explora cómo cambian las estimaciones en perfiles históricos hipotéticos. Uso académico y de planificación agregada.</PageTitle>
    <div className="two-col">
      <section className="panel"><h2>Perfil del caso</h2><form onSubmit={submit} className="form-grid">
        <label>Edad en años<input name="edad" type="number" min="0" max="110" defaultValue="72" required/></label>
        <label>Sexo registrado<select name="sexo" defaultValue="M"><option value="F">Femenino</option><option value="M">Masculino</option></select></label>
        <Department name="departamento"/>
        <label>Fecha histórica<input type="date" name="fecha" defaultValue="2021-04-15" required/></label>
        <button className="primary full" disabled={busy}>{busy ? 'Calculando…' : 'Calcular riesgo'}</button>
        {error && <p className="error full" role="alert">{error}</p>}
      </form></section>
      <section className="panel" aria-live="polite"><h2>Resultado de la simulación</h2>
        {!result ? <p className="notice">{busy ? 'Consultando el modelo…' : 'Completa el perfil y calcula el riesgo.'}</p> : <>
          <div className="prediction"><span className="pill">{result.nivel_riesgo}</span><strong>{percent(result.letalidad_estimada)}</strong><span>Letalidad estimada</span></div>
          <p>Periodo {result.periodo_epidemiologico} · Modelo v{result.modelo_version} · {number(result.latencia_ms, 3)} ms</p>
          <Table columns={['Factor', 'Valor', 'Aporte al logit']} rows={list(result.factores).map(f => [f.variable, f.variable, f.valor, number(f.aporte_logit, 3)])}/>
          <p className="footnote">El resultado describe un ejercicio histórico. La valoración clínica corresponde a un profesional de salud.</p>
        </>}
      </section>
    </div>
  </>;
}

export function Training({ api, live, admin }) {
  const cluster = useData(api, '/api/cluster', live.revision);
  const jobs = useData(api, '/api/entrenamientos?limit=10', live.revision);
  const model = useData(api, '/api/modelo', live.revision);
  const [submitted, setSubmitted] = useState();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const current = new Map();
  for (const job of [submitted, live.entrenamiento, ...list(jobs.data)].filter(Boolean)) {
    const previous = current.get(job.id);
    if (!previous || (active(previous) && (!active(job) || (job.historial?.length ?? 0) > (previous.historial?.length ?? 0)))) {
      current.set(job.id, job);
    }
  }
  const visible = [...current.values()].sort((a, b) => Date.parse(b.creado) - Date.parse(a.creado));
  const latest = visible[0];
  const health = live.estado || {};
  const clusterLive = Array.isArray(live.cluster) ? live.cluster : cluster.data?.nodos;
  const nodes = clusterLive?.filter(n => n.conectado).length ?? cluster.data?.nodos_activos ?? 0;
  const locked = active(latest) || (!latest && (cluster.data?.entrenando || health.entrenando));
  const metrics = model.data?.metricas;
  const history = latest?.historial || model.data?.historial || [];
  async function submit(event) {
    event.preventDefault();
    setError('');
    setBusy(true);
    const params = Object.fromEntries(Array.from(new FormData(event.currentTarget), ([key, value]) => [key, Number(value)]));
    try { setSubmitted(await api('/api/entrenamientos', {body: params})); jobs.reload(); }
    catch (e) { setError(e.message); }
    finally { setBusy(false); }
  }
  return <>
    <PageTitle title="Entrenamiento distribuido">Consulta el modelo activo y sigue la pérdida por época mientras el clúster calcula los gradientes.</PageTitle>
    <Notice state={model}><div className="stats"><Stat title="Modelo activo" value={'v' + model.data?.version}/>
      <Stat title="AUC" value={number(metrics?.AUC, 4)}/><Stat title="F1" value={number(metrics?.F1, 4)}/><Stat title="Registros de prueba" value={number(model.data?.n_test)}/></div></Notice>
    <div className={admin ? 'two-col' : ''}>
      {admin && <section className="panel"><h2>Nuevo entrenamiento</h2>
        <form className="form-grid" onSubmit={submit}>
          <label>Nodos activos<select name="nodos" defaultValue="0"><option value="0">Todos ({nodes})</option>{Array.from({length: nodes}, (_, i) => <option key={i}>{i + 1}</option>)}</select></label>
          <label>Épocas<input name="epocas" type="number" min="1" max="50" defaultValue="6" required/></label>
          <label>Lote global<input name="lote_global" type="number" min="256" max="1048576" defaultValue="8192" required/></label>
          <label>Tasa de aprendizaje<input name="tasa_aprendizaje" type="number" min="0.0001" max="10" step="any" defaultValue="0.1" required/></label>
          <button className="primary full" disabled={busy || locked || !nodes}>{locked ? 'Entrenamiento en curso' : busy ? 'Enviando…' : 'Entrenar modelo'}</button>
          {error && <p className="error full" role="alert">{error}</p>}
        </form>
      </section>}
      <section className="panel"><h2>Progreso por época</h2>
        {latest && <p role="status">Estado <strong>{friendly(latest.estado)}</strong>{latest.error && ' · ' + latest.error}</p>}
        <Table columns={['Época', 'Pérdida', 'Duración (s)']} rows={history.map(r => [r.epoca, r.epoca, number(r.perdida, 5), number(r.duracion_s, 3)])}/>
      </section>
    </div>
    <section className="panel"><div className="panel-head"><h2>Historial de entrenamientos</h2><button onClick={jobs.reload}>Actualizar</button></div>
      <Notice state={jobs}><Table columns={['Inicio', 'Estado', 'Nodos', 'Modelo', 'Duración (s)']}
        rows={visible.map(r => [r.id, clock(r.inicio || r.creado), friendly(r.estado), r.nodos_solicitados, r.modelo_version ? 'v' + r.modelo_version : '—', number(r.duracion_s, 2)])}/></Notice>
    </section>
  </>;
}

export function Admin({ api, live }) {
  const [clockTick, setClockTick] = useState(0);
  useEffect(() => {
    const timer = setInterval(() => setClockTick(n => n + 1), 5000);
    return () => clearInterval(timer);
  }, []);
  const snapshot = useData(api, '/api/cluster', live.revision);
  const apiMetrics = useData(api, '/api/metricas', live.revision + clockTick);
  const nodes = Array.isArray(live.cluster) ? live.cluster : list(snapshot.data?.nodos);
  const metrics = apiMetrics.data;
  const latency = list(metrics?.latencias).find(route => route.ruta === 'POST /api/predicciones');
  const events = live.events.filter(e => ['cluster', 'entrenamiento', 'estado'].includes(e.tipo));
  function describe(event) {
    const d = event.datos;
    if (event.tipo === 'cluster') return d.filter(n => n.conectado).length + '/' + d.length + ' conectados';
    if (event.tipo === 'entrenamiento') return friendly(d.estado) + (d.error ? ' · ' + d.error : '');
    return friendly(d.estado);
  }
  return <>
    <PageTitle title="Administración del clúster">Supervisa latidos, carga de CPU y tiempos de respuesta del coordinador.</PageTitle>
    <div className="stats"><Stat title="Nodos conectados" value={nodes.filter(n => n.conectado).length + ' / ' + nodes.length}/>
      <Stat title="Predicción p95 (ms)" value={number(latency?.p95_ms, 3)}/><Stat title="Aciertos de caché" value={percent(metrics?.tasa_cache)}/>
      <Stat title="Memoria API (MiB)" value={number(metrics?.heap_mb, 2)}/></div>
    <section className="panel"><h2>Nodos de cómputo</h2>
      <Notice state={snapshot}><Table columns={['Nodo', 'Conexión', 'CPU (%)', 'RTT (ms)', 'Registros', 'Último latido']}
        rows={nodes.map(n => [n.id, n.id, n.conectado ? 'Conectado' : 'Desconectado', number(n.estado?.CPUPct, 2), number(n.rtt_ms, 3), number(n.estado?.Registros), clock(n.ultimo_latido)])}/></Notice>
    </section>
    <section className="panel"><h2>Latencia por ruta</h2>
      <Notice state={apiMetrics}><Table columns={['Ruta', 'Peticiones', 'p50 (ms)', 'p95 (ms)', 'Máx. (ms)', 'Errores 5xx']}
        rows={list(metrics?.latencias).filter(r => r.ruta.startsWith('GET /api/') || r.ruta.startsWith('POST /api/'))
          .map(r => [r.ruta, r.ruta, number(r.peticiones), number(r.p50_ms, 3), number(r.p95_ms, 3), number(r.max_ms, 3), number(r.errores_5xx)])}/></Notice>
    </section>
    <section className="panel"><h2>Registro de eventos de esta sesión</h2>
      <Table columns={['Hora', 'Evento', 'Detalle']} rows={events.map((e, i) => [e.ts + i, clock(e.ts), e.tipo, describe(e)])}/>
    </section>
  </>;
}
