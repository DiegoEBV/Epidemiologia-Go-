#!/usr/bin/env python3
"""Benchmark del entrenamiento distribuido (PC4).

Lanza, vía la API, un entrenamiento con 1, 2, 3 y 4 nodos, espera a que
termine y guarda tiempos, speedup y métricas en results/pc4/bench_cluster.json.
Solo usa la biblioteca estándar.

    python3 scripts/bench_cluster.py --url http://localhost:8080 --nodos 1,2,3,4
"""
import argparse, json, time, urllib.request

def llamar(url, metodo="GET", cuerpo=None, token=None):
    datos = json.dumps(cuerpo).encode() if cuerpo is not None else None
    req = urllib.request.Request(url, data=datos, method=metodo)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read())

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", default="http://localhost:8080")
    ap.add_argument("--nodos", default="1,2,3,4")
    ap.add_argument("--epocas", type=int, default=6)
    ap.add_argument("--out", default="results/pc4/bench_cluster.json")
    a = ap.parse_args()
    tok = llamar(a.url + "/api/auth/login", "POST", {"username": "admin", "password": "admin123"})["token"]
    filas = []
    for n in [int(x) for x in a.nodos.split(",")]:
        job = llamar(a.url + "/api/entrenamientos", "POST", {"nodos": n, "epocas": a.epocas}, tok)
        print(f"nodos={n}: trabajo {job['id']}", flush=True)
        while True:
            time.sleep(1)
            job = llamar(a.url + "/api/entrenamientos/" + job["id"], token=tok)
            if job["estado"] in ("completado", "error"):
                break
        if job["estado"] == "error":
            raise SystemExit(f"falló: {job.get('error')}")
        mods = llamar(a.url + "/api/modelos?limit=1", token=tok)
        m = mods[0]
        fila = {"nodos": n, "entrenamiento_s": m["duracion_s"], "distribucion_s": m["distribucion"]["distribucion_s"],
                "total_trabajo_s": job["duracion_s"], "pasos_epoca": m["distribucion"]["pasos_epoca"],
                "lote_local": m["distribucion"]["lote_local"], "auc": m["metricas"]["AUC"],
                "perdida_final": m["historial"][-1]["perdida"], "modelo_version": m["version"],
                "detalle_nodos": m["distribucion"]["nodos"]}
        filas.append(fila)
        print(f"  entrenamiento {fila['entrenamiento_s']:.2f}s · distribución {fila['distribucion_s']:.2f}s · AUC {fila['auc']:.4f}", flush=True)
    base = filas[0]["entrenamiento_s"]
    for f in filas:
        f["speedup"] = base / f["entrenamiento_s"]
        f["eficiencia"] = f["speedup"] / f["nodos"]
    json.dump({"fecha": time.strftime("%Y-%m-%dT%H:%M:%S"), "epocas": a.epocas, "resultados": filas},
              open(a.out, "w"), indent=2, ensure_ascii=False)
    print(f"\n{'nodos':>5} {'train s':>8} {'speedup':>8} {'efic.':>6} {'AUC':>7}")
    for f in filas:
        print(f"{f['nodos']:>5} {f['entrenamiento_s']:>8.2f} {f['speedup']:>8.2f} {100*f['eficiencia']:>5.0f}% {f['auc']:>7.4f}")

if __name__ == "__main__":
    main()
