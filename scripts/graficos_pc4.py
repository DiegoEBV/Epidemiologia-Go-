#!/usr/bin/env python3
"""Gráficos de la PC4 a partir de results/pc4/*.json.

    python3 scripts/graficos_pc4.py results/pc4 results/figs
"""
import json, os, sys
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

ent, sal = sys.argv[1], sys.argv[2]
os.makedirs(sal, exist_ok=True)
J = lambda n: json.load(open(os.path.join(ent, n), encoding="utf-8"))
AZUL, NAR, VERDE, GRIS, ROJO = "#1f5fa8", "#e07b24", "#2e8b57", "#8a8f98", "#c0392b"
plt.rcParams.update({"font.size": 10, "axes.spines.top": False, "axes.spines.right": False,
                     "axes.grid": True, "grid.alpha": .25, "figure.dpi": 150})

def guardar(fig, n):
    fig.tight_layout()
    fig.savefig(os.path.join(sal, n), bbox_inches="tight")
    plt.close(fig)
    print("  ✓", n)

# 1. Escalamiento del entrenamiento distribuido
b = J("bench_cluster.json")["resultados"]
n = [r["nodos"] for r in b]
fig, (a1, a2) = plt.subplots(1, 2, figsize=(10, 3.8))
a1.bar(n, [r["entrenamiento_s"] for r in b], color=AZUL, width=.6, label="entrenamiento (6 épocas)")
a1.bar(n, [r["distribucion_s"] for r in b], bottom=[r["entrenamiento_s"] for r in b], color=NAR, width=.6, label="envío de fragmentos por TCP")
for x, r in zip(n, b):
    a1.text(x, r["entrenamiento_s"] + r["distribucion_s"] + .1, f'{r["entrenamiento_s"]:.2f} s', ha="center", fontsize=9)
a1.set_xlabel("nodos del clúster (1 CPU cada uno)"); a1.set_ylabel("segundos"); a1.set_xticks(n)
a1.set_title("Tiempo de entrenamiento distribuido"); a1.legend(fontsize=8, frameon=False)
a2.plot(n, n, "--", color=GRIS, label="ideal (lineal)")
a2.plot(n, [r["speedup"] for r in b], "o-", color=VERDE, lw=2, label="medido")
for x, r in zip(n, b):
    a2.annotate(f'{r["speedup"]:.2f}× ({100*r["eficiencia"]:.0f}%)', (x, r["speedup"]), textcoords="offset points", xytext=(0, -15), ha="center", fontsize=8.5)
a2.set_xlabel("nodos"); a2.set_ylabel("speedup"); a2.set_xticks(n); a2.set_title("Speedup y eficiencia"); a2.legend(fontsize=8, frameon=False)
guardar(fig, "pc4_bench_cluster.png")

# 2. Latencia: histograma de las predicciones bajo carga
L = J("latencia.json")
pred = next(r for r in L["resumenes"] if r["ruta"] == "POST /api/predicciones")
lim = pred["limites_ms"]
etq = [f"<{lim[0]}"] + [f"{lim[i]}–{lim[i+1]}" for i in range(len(lim) - 1)] + [f"≥{lim[-1]}"]
tot = sum(pred["histograma"])
fig, ax = plt.subplots(figsize=(9, 3.6))
cols = [VERDE if i < len(lim) - 1 else ROJO for i in range(len(etq))]
bars = ax.bar(range(len(etq)), [100 * h / tot for h in pred["histograma"]], color=cols)
for i, h in enumerate(pred["histograma"]):
    if h:
        ax.text(i, 100 * h / tot + 1, f"{100*h/tot:.1f}%", ha="center", fontsize=8.5)
ax.set_xticks(range(len(etq))); ax.set_xticklabels(etq)
ax.axvline(len(lim) - 1.5, color=ROJO, ls="--", lw=1)
ax.text(len(lim) - 1.4, 0.8, "límite 100 ms", color=ROJO, fontsize=8.5, transform=ax.get_xaxis_transform())
ax.set_xlabel("latencia (ms)"); ax.set_ylabel("% de peticiones")
miles = lambda v: f"{v:,.0f}".replace(",", " ")
ax.set_title(f'POST /api/predicciones · {miles(pred["peticiones"])} peticiones · {L["concurrencia"]} clientes · {miles(pred["rps"])} req/s · '
             f'p50 {pred["p50_ms"]:.2f} ms · p99 {pred["p99_ms"]:.2f} ms', fontsize=9.5)
guardar(fig, "pc4_latencia_hist.png")

# 3. Percentiles por ruta: normal vs durante entrenamiento
E = J("latencia_entrenando.json")
rutas = ["POST /api/predicciones", "GET /api/impacto", "GET /api/cluster"]
fig, ax = plt.subplots(figsize=(9, 3.6))
w = .13
for k, (datos, nombre, tono) in enumerate([(L, "reposo", 0), (E, "entrenando", 1)]):
    m = {r["ruta"]: r for r in datos["resumenes"]}
    for j, (p, c) in enumerate([("p50_ms", AZUL), ("p95_ms", NAR), ("p99_ms", ROJO)]):
        xs = [i + (k * 3 + j - 2.5) * w for i in range(len(rutas))]
        ax.bar(xs, [m[r][p] for r in rutas], w * .95, color=c, alpha=1 if k == 0 else .55,
               label=f'{p[:3]} {nombre}', hatch=None if k == 0 else "//", edgecolor="white")
ax.set_xticks(range(len(rutas))); ax.set_xticklabels(rutas); ax.set_ylabel("ms")
ax.set_title("Percentiles de latencia por endpoint (todos muy por debajo de 100 ms)")
ax.legend(ncol=2, fontsize=8, frameon=False)
guardar(fig, "pc4_latencia_rutas.png")

# 4. Impacto: top 15 provincias
I = J("api_impacto_top15.json")["provincias"][::-1]
fig, ax = plt.subplots(figsize=(9, 5))
ax.barh([f'{p["provincia"]} ({p["departamento"]})' for p in I], [p["graves_esperados"] for p in I], color=ROJO, alpha=.85)
for i, p in enumerate(I):
    ax.text(p["graves_esperados"] + 2, i, f'{p["graves_esperados"]:.1f}  ·  {p["tasa_por_1000"]:.1f}‰', va="center", fontsize=8)
ax.set_xlabel("casos graves esperados (Σ letalidad estimada por caso)")
ax.set_title(f'Provincias con más casos graves esperados (casos positivos desde {I[0]["mes_desde"]}) · calculado en el clúster', fontsize=9.5)
ax.grid(axis="y", alpha=0)
guardar(fig, "pc4_impacto_top.png")
