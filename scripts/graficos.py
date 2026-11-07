"""Genera las figuras del informe a partir de results/resultados.json.

Uso: python3 scripts/graficos.py results/resultados.json results/figs
"""
import json
import os
import sys

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.ticker
from matplotlib.patches import FancyBboxPatch

src = sys.argv[1] if len(sys.argv) > 1 else "results/resultados.json"
out = sys.argv[2] if len(sys.argv) > 2 else "results/figs"
os.makedirs(out, exist_ok=True)
R = json.load(open(src))

AZUL, ROJO, GRIS, VERDE = "#2563a8", "#c0392b", "#7f8c8d", "#2e8b57"
plt.rcParams.update({"font.size": 10, "axes.spines.top": False, "axes.spines.right": False,
                     "figure.dpi": 150, "savefig.bbox": "tight"})
DEPTS = ["AMAZONAS", "ANCASH", "APURIMAC", "AREQUIPA", "AYACUCHO", "CAJAMARCA", "CALLAO", "CUSCO",
         "HUANCAVELICA", "HUANUCO", "ICA", "JUNIN", "LA LIBERTAD", "LAMBAYEQUE", "LIMA", "LORETO",
         "MADRE DE DIOS", "MOQUEGUA", "PASCO", "PIURA", "PUNO", "SAN MARTIN", "TACNA", "TUMBES", "UCAYALI"]
GRUPOS = ["0-17", "18-29", "30-39", "40-49", "50-59", "60-69", "70-79", "80+"]
E = R["eda"]

# 1. Serie mensual.
meses = sorted(E["PorMes"].keys(), key=int)
lab = [f"{m[4:6]}/{m[2:4]}" for m in meses]
pos = [E["PorMes"][m][0] for m in meses]
fal = [E["PorMes"][m][1] for m in meses]
fig, ax = plt.subplots(figsize=(8, 3.2))
ax.bar(range(len(meses)), pos, color=AZUL, alpha=.8, label="Casos positivos")
ax.set_ylabel("Casos positivos / mes")
ax.yaxis.set_major_formatter(matplotlib.ticker.FuncFormatter(lambda v, _: f"{v:,.0f}"))
ax2 = ax.twinx()
ax2.plot(range(len(meses)), fal, color=ROJO, lw=2, label="Fallecidos")
ax2.set_ylabel("Fallecidos / mes", color=ROJO)
ax2.spines["top"].set_visible(False)
step = max(1, len(meses) // 12)
ax.set_xticks(range(0, len(meses), step))
ax.set_xticklabels(lab[::step], rotation=45)
ax.set_title("Casos positivos y fallecidos por mes")
h1, l1 = ax.get_legend_handles_labels()
h2, l2 = ax2.get_legend_handles_labels()
ax.legend(h1 + h2, l1 + l2, loc="upper right", frameon=False)
fig.savefig(f"{out}/eda_mensual.png")
plt.close(fig)

# 2. Letalidad por edad + distribución de edad.
fig, (a1, a2) = plt.subplots(1, 2, figsize=(9, 3.2))
tot = sum(E["PorEdad"][0])
a1.bar(GRUPOS, [100 * v / tot for v in E["PorEdad"][0]], color=AZUL)
a1.set_title("Distribución de casos positivos por edad")
a1.set_ylabel("% de casos")
a1.tick_params(axis="x", rotation=45)
a2.bar(GRUPOS, E["Letalidad"], color=ROJO)
for i, v in enumerate(E["Letalidad"]):
    a2.text(i, v + 0.8, f"{v:.1f}", ha="center", fontsize=8)
a2.set_title("Letalidad (fallecidos / positivos) por edad")
a2.set_ylabel("%")
a2.tick_params(axis="x", rotation=45)
fig.tight_layout()
fig.savefig(f"{out}/eda_edad.png")
plt.close(fig)

# 3. Letalidad por departamento.
orden = sorted(range(25), key=lambda i: E["LetalDept"][i])
fig, ax = plt.subplots(figsize=(7, 5.5))
ax.barh([DEPTS[i] for i in orden], [E["LetalDept"][i] for i in orden], color=AZUL)
ax.set_xlabel("Letalidad (%)")
ax.set_title("Letalidad por departamento")
fig.savefig(f"{out}/eda_departamento.png")
plt.close(fig)

# 4. Limpieza.
fig, ax = plt.subplots(figsize=(7, 2.8))
nombres = R["descarte_nombres"]
vals = [sum(st["Descartes"][i] for st in R["limpieza"]) for i in range(len(nombres))]
idx = [i for i, v in enumerate(vals) if v > 0]
ax.barh([nombres[i] for i in idx], [vals[i] for i in idx], color=GRIS)
for j, i in enumerate(idx):
    ax.text(vals[i], j, f" {vals[i]:,}", va="center", fontsize=8)
ax.set_title("Registros descartados por regla de limpieza")
ax.invert_yaxis()
fig.savefig(f"{out}/limpieza.png")
plt.close(fig)

# 5. Benchmark de carga.
bc = R["bench_carga"]
labs = ["Secuencial"] + [f"{b['workers']} workers" for b in bc[1:]]
fig, ax = plt.subplots(figsize=(7, 3))
bars = ax.bar(labs, [b["segundos"] for b in bc], color=[GRIS] + [AZUL] * (len(bc) - 1))
for b, d in zip(bars, bc):
    ax.text(b.get_x() + b.get_width() / 2, b.get_height(), f"{d['segundos']:.2f}s\n×{d['speedup']:.2f}",
            ha="center", va="bottom", fontsize=8)
ax.set_ylabel("Segundos (mediana de 3)")
ax.set_title(f"Carga y limpieza de {R['limpieza'][0]['Leidas']:,} filas ({R['cpus']} núcleos)")
ax.set_ylim(0, max(b["segundos"] for b in bc) * 1.3)
fig.savefig(f"{out}/bench_carga.png")
plt.close(fig)

# 6. Benchmark de entrenamiento.
bt = R["bench_train"]
fig, (a1, a2) = plt.subplots(1, 2, figsize=(9, 3))
a1.bar([str(b["workers"]) for b in bt], [b["segundos"] for b in bt], color=AZUL)
a1.set_xlabel("Workers")
a1.set_ylabel("Segundos")
a1.set_title("Tiempo de entrenamiento (6 épocas)")
w = [b["workers"] for b in bt]
a2.plot(w, [b["speedup"] for b in bt], "o-", color=AZUL, label="Speedup medido")
a2.plot(w, [min(x, R["cpus"]) for x in w], "--", color=GRIS, label=f"Máximo con {R['cpus']} núcleos")
a2.set_xlabel("Workers")
a2.set_ylabel("Speedup")
a2.set_title("Speedup del entrenamiento")
a2.legend(frameon=False)
fig.tight_layout()
fig.savefig(f"{out}/bench_train.png")
plt.close(fig)

# 7. Curva de pérdida.
h = R["historial"]
fig, ax = plt.subplots(figsize=(5, 3))
ax.plot([x["Epoca"] for x in h], [x["Perdida"] for x in h], "o-", color=AZUL)
ax.set_xlabel("Época")
ax.set_ylabel("Entropía cruzada ponderada")
ax.set_title("Convergencia del entrenamiento")
fig.savefig(f"{out}/perdida.png")
plt.close(fig)

# 8. Impacto.
imp = R["impacto_top"][:10][::-1]
fig, ax = plt.subplots(figsize=(7, 3.6))
ax.barh([f"{r['Provincia'].title()} ({r['Departamento'].title()})" for r in imp],
        [r["GravesEsperados"] for r in imp], color=ROJO)
ax.set_xlabel("Casos graves esperados")
ax.set_title(f"Top 10 provincias por casos graves esperados (casos desde {R['impacto_mes_desde']})")
fig.savefig(f"{out}/impacto.png")
plt.close(fig)


# 9 y 10. Diagramas de arquitectura.
def caja(ax, x, y, w, hgt, txt, color, fs=8.5):
    ax.add_patch(FancyBboxPatch((x, y), w, hgt, boxstyle="round,pad=0.02", fc=color, ec="#333", lw=1))
    ax.text(x + w / 2, y + hgt / 2, txt, ha="center", va="center", fontsize=fs)


def flecha(ax, x1, y1, x2, y2, txt=""):
    ax.annotate("", (x2, y2), (x1, y1), arrowprops=dict(arrowstyle="->", lw=1.2, color="#333"))
    if txt:
        ax.text((x1 + x2) / 2, (y1 + y2) / 2 + 0.12, txt, ha="center", fontsize=7, color="#333")


fig, ax = plt.subplots(figsize=(9, 3.6))
ax.set_xlim(0, 10)
ax.set_ylim(0, 4.2)
ax.axis("off")
caja(ax, 0.1, 1.6, 1.6, 1.0, "CSV MINSA\npositivos (~4.5M)\nfallecidos (~221k)", "#f6e3c4")
caja(ax, 2.2, 1.6, 1.6, 1.0, "main: lee header,\narma Esquema y\nparte en 4W bloques", "#dbe8f7")
flecha(ax, 1.7, 2.1, 2.2, 2.1)
for i, y in enumerate([3.2, 2.1, 1.0]):
    caja(ax, 4.4, y - 0.35, 1.7, 0.7, f"Worker {i + 1 if i < 2 else 'W'}\nparse + limpieza\n(Parser local)", "#d5efe0", 7.5)
    flecha(ax, 3.8, 2.1, 4.4, y)
    flecha(ax, 6.1, y, 6.7, 2.1)
ax.text(3.55, 3.05, "chan bloque", ha="center", fontsize=7.5)
ax.text(6.75, 3.05, "chan Lote\n(buffered)", ha="center", fontsize=7.5)
ax.text(5.25, 1.38, "⋮", ha="center", fontsize=12)
caja(ax, 6.7, 1.6, 1.5, 1.0, "Colector\n(fan-in)\nappend []Record", "#dbe8f7")
caja(ax, 8.5, 1.6, 1.4, 1.0, "Dataset limpio\n+ Stats\n+ EDA", "#f6e3c4")
flecha(ax, 8.2, 2.1, 8.5, 2.1)
ax.text(5, 0.15, "sync.WaitGroup cierra el canal de salida cuando terminan todos los workers · "
        "cada worker escribe solo en su propio slot de Stats", ha="center", fontsize=7.5, style="italic")
fig.savefig(f"{out}/arq_cargador.png")
plt.close(fig)

fig, ax = plt.subplots(figsize=(9, 4))
ax.set_xlim(0, 10)
ax.set_ylim(0, 4.6)
ax.axis("off")
caja(ax, 3.6, 3.3, 2.8, 1.0, "Coordinador (Parameter Server)\nw, b globales · única goroutine\nque escribe los pesos", "#f6e3c4")
for i, x in enumerate([0.4, 2.8, 5.2, 7.6]):
    caja(ax, x, 0.6, 2.0, 1.1, f"Worker {i + 1}\nshard {i + 1} del train\nmini-lote B/P → ∇L", "#d5efe0", 8)
    ax.annotate("", (x + 0.7, 1.7), (4.4 + i * 0.4, 3.3), arrowprops=dict(arrowstyle="->", color=AZUL, lw=1.2))
    ax.annotate("", (5.6 - 0.4 * (3 - i), 3.3), (x + 1.3, 1.7), arrowprops=dict(arrowstyle="->", color=ROJO, lw=1.2))
ax.text(1.2, 2.65, "MsgPesos (w, b)\nbroadcast", color=AZUL, fontsize=8)
ax.text(7.4, 2.65, "MsgGradiente (∇w, ∇b)\nbarrera: espera P", color=ROJO, fontsize=8)
ax.text(5, 0.15, "w ← w − η(Σ∇/Σω + λw)  ·  PC3: canales Go  ·  PC4: los mismos mensajes viajan por TCP entre contenedores",
        ha="center", fontsize=8, style="italic")
fig.savefig(f"{out}/arq_entrenamiento.png")
plt.close(fig)
print("figuras en", out, sorted(os.listdir(out)))
