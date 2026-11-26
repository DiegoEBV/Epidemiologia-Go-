import React, { useCallback, useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { request, stream } from './api';
import { Admin, Dashboard, Prediction, Training } from './panels';
import './style.css';

const pages = [['resumen', 'Panorama'], ['simulacion', 'Simulación'], ['entrenamiento', 'Entrenamiento'], ['admin', 'Clúster']];

function Brand() {
  return <div className="brand"><span className="brand-icon" aria-hidden="true">✚</span><span>Observatorio<br/><strong>COVID-19 Perú</strong></span></div>;
}

function Login({ onLogin, expired }) {
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  async function submit(event) {
    event.preventDefault();
    const data = Object.fromEntries(new FormData(event.currentTarget));
    setError('');
    setBusy(true);
    try { onLogin(await request('/api/auth/login', {body: data})); }
    catch (e) { setError(e.message); }
    finally { setBusy(false); }
  }
  return <main className="login"><section className="login-intro">
    <Brand/>
    <div><div className="eyebrow">Análisis epidemiológico histórico</div>
    <h1>Riesgo de COVID-19<br/>en el Perú</h1>
    <p>Explora la carga estimada por provincia y compara escenarios con el modelo distribuido.</p></div>
    <div className="login-footer">Proyecto académico · CC65</div>
  </section><section className="login-side">
    <form onSubmit={submit} className="login-form">
      <h2>Iniciar sesión</h2>
      <label>Usuario<input name="username" autoComplete="username" required autoFocus/></label>
      <label>Contraseña<input name="password" type="password" autoComplete="current-password" required/></label>
      {(error || expired) && <p className="error" role="alert">{error || 'La sesión venció. Vuelve a iniciar sesión.'}</p>}
      <button className="primary" disabled={busy}>{busy ? 'Ingresando…' : 'Ingresar'}</button>
    </form>
  </section></main>;
}

function Workspace({ session, onLogout }) {
  const [page, setPage] = useState('resumen');
  const [connection, setConnection] = useState('Conectando');
  const [live, setLive] = useState({ events: [], revision: 0 });
  const admin = session.rol === 'admin';
  const api = useCallback(async (path, options) => {
    try { return await request(path, {...options, token: session.token}); }
    catch (e) {
      if (e.status === 401) onLogout(true);
      throw e;
    }
  }, [session.token, onLogout]);

  useEffect(() => {
    let socket;
    let retryTimer;
    let stopped = false;
    let delay = 1000;
    const exp = setTimeout(() => onLogout(true), Math.max(0, Date.parse(session.expira) - Date.now()));
    function connect() {
      socket = stream(session.token);
      socket.onopen = () => {
        delay = 1000;
        setConnection('En vivo');
        setLive(prev => ({...prev, revision: prev.revision + 1}));
      };
      socket.onmessage = ({data}) => {
        const event = JSON.parse(data);
        setLive(prev => ({
          ...prev,
          [event.tipo]: event.datos,
          events: [event, ...prev.events].slice(0, 20),
          revision: prev.revision + Number(event.tipo === 'entrenamiento' && event.datos.estado === 'completado'),
        }));
      };
      socket.onclose = () => {
        if (stopped) return;
        setConnection('Reconectando');
        retryTimer = setTimeout(connect, delay);
        delay = Math.min(15000, delay * 2);
      };
    }
    connect();
    return () => {
      stopped = true;
      clearTimeout(exp);
      clearTimeout(retryTimer);
      socket?.close();
    };
  }, [session, onLogout]);

  const main = useRef(null);
  function navigate(next) {
    setPage(next);
    main.current?.focus({preventScroll: true});
    window.scrollTo(0, 0);
  }

  return <div className="workspace">
    <a className="skip" href="#contenido">Ir al contenido</a>
    <aside className="sidebar">
      <Brand/>
      <nav aria-label="Módulos">
        {pages.filter(([id]) => id !== 'admin' || admin).map(([id, title]) =>
          <button key={id} onClick={() => navigate(id)} aria-current={page === id ? 'page' : undefined}>
            {title}
          </button>)}
      </nav>
      <div className="session"><span>{session.usuario}</span><small>{admin ? 'Administrador' : 'Analista'}</small>
      <button onClick={() => onLogout(false)}>Cerrar sesión</button></div>
    </aside>
    <main className="main" id="contenido" ref={main} tabIndex={-1}>
      <header className="toolbar"><a href="/swagger/" target="_blank" rel="noreferrer">Documentación API ↗</a>
      <span className={'status ' + (connection === 'En vivo' ? 'online' : '')} role="status">{connection}</span></header>
      {page === 'resumen' && <Dashboard api={api} live={live}/>}
      {page === 'simulacion' && <Prediction api={api}/>}
      {page === 'entrenamiento' && <Training api={api} live={live} admin={admin}/>}
      {page === 'admin' && admin && <Admin api={api} live={live}/>}
    </main>
  </div>;
}

function App() {
  const [session, setSession] = useState(null);
  const [expired, setExpired] = useState(false);
  const logout = useCallback(value => {
    setExpired(value);
    setSession(null);
  }, []);
  return session ? <Workspace session={session} onLogout={logout}/> : <Login onLogin={setSession} expired={expired}/>;
}

createRoot(document.getElementById('root')).render(<App/>);
