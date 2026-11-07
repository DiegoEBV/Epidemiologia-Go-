const fs = require("fs");
const path = require("path");
const {
  Document, Packer, Paragraph, TextRun, ImageRun, Table, TableRow, TableCell, AlignmentType,
  HeadingLevel, LevelFormat, WidthType, ShadingType, BorderStyle, TableOfContents, PageBreak,
  Footer, PageNumber, ExternalHyperlink,
} = require("docx");

const ROOT = path.join(__dirname, "..");
const R = JSON.parse(fs.readFileSync(path.join(ROOT, "results/resultados.json"), "utf8"));
const FIG = (f) => fs.readFileSync(path.join(ROOT, "results/figs", f));
const E = R.eda;
const M = R.metricas;
const fmt = (n, d = 0) => Number(n).toLocaleString("es-PE", { minimumFractionDigits: d, maximumFractionDigits: d });
const pct = (x, d = 1) => fmt(100 * x, d) + "%";

const FONT = "Arial";
const W = 9026; // ancho útil A4 con márgenes de 1"

// ---------- helpers ----------
function runs(text, base = {}) {
  // **negrita**, `código`, ==resaltado==
  const out = [];
  const re = /(\*\*[^*]+\*\*|`[^`]+`|==[^=]+==)/g;
  let last = 0, m;
  while ((m = re.exec(text))) {
    if (m.index > last) out.push(new TextRun({ text: text.slice(last, m.index), ...base }));
    const t = m[0];
    if (t.startsWith("**")) out.push(new TextRun({ text: t.slice(2, -2), bold: true, ...base }));
    else if (t.startsWith("`")) out.push(new TextRun({ text: t.slice(1, -1), font: "Consolas", size: 19, ...base }));
    else out.push(new TextRun({ text: t.slice(2, -2), highlight: "yellow", ...base }));
    last = m.index + t.length;
  }
  if (last < text.length) out.push(new TextRun({ text: text.slice(last), ...base }));
  return out;
}
const P = (t, o = {}) => new Paragraph({ children: runs(t), spacing: { after: 120, line: 300 }, alignment: AlignmentType.JUSTIFIED, ...o });
const H1 = (t) => new Paragraph({ heading: HeadingLevel.HEADING_1, children: [new TextRun(t)] });
const H2 = (t) => new Paragraph({ heading: HeadingLevel.HEADING_2, children: [new TextRun(t)] });
const H3 = (t) => new Paragraph({ heading: HeadingLevel.HEADING_3, children: [new TextRun(t)] });
const B = (t, lvl = 0) => new Paragraph({ numbering: { reference: "bul", level: lvl }, children: runs(t), spacing: { after: 60, line: 290 } });
const N = (t, ref = "num") => new Paragraph({ numbering: { reference: ref, level: 0 }, children: runs(t), spacing: { after: 60, line: 290 } });
const FORM = (t) => new Paragraph({ alignment: AlignmentType.CENTER, spacing: { before: 60, after: 100 },
  children: [new TextRun({ text: t, font: "Cambria Math", size: 22, italics: true })] });
const CAP = (t) => new Paragraph({ alignment: AlignmentType.CENTER, spacing: { after: 200 },
  children: [new TextRun({ text: t, italics: true, size: 18, color: "555555" })] });
const PB = () => new Paragraph({ children: [new PageBreak()] });

let figN = 0, tabN = 0;
function IMG(file, wIn, hIn, caption) {
  figN++;
  return [
    new Paragraph({ alignment: AlignmentType.CENTER, spacing: { before: 120, after: 60 }, keepNext: true,
      children: [new ImageRun({ type: "png", data: FIG(file), transformation: { width: wIn * 96, height: hIn * 96 } })] }),
    CAP(`Figura ${figN}. ${caption}`),
  ];
}
const border = { style: BorderStyle.SINGLE, size: 4, color: "BBBBBB" };
const borders = { top: border, bottom: border, left: border, right: border };
function TABLE(headers, rows, widths, caption) {
  tabN++;
  const total = widths.reduce((a, b) => a + b, 0);
  const cell = (t, i, head) => new TableCell({
    borders, width: { size: widths[i], type: WidthType.DXA },
    shading: head ? { fill: "1F4E79", type: ShadingType.CLEAR } : undefined,
    margins: { top: 60, bottom: 60, left: 100, right: 100 },
    children: [new Paragraph({ alignment: i === 0 ? AlignmentType.LEFT : AlignmentType.CENTER,
      children: runs(String(t), head ? { bold: true, color: "FFFFFF", size: 19 } : { size: 19 }) })],
  });
  return [
    new Paragraph({ keepNext: true, spacing: { before: 120, after: 60 },
      children: [new TextRun({ text: `Tabla ${tabN}. ${caption}`, bold: true, size: 19 })] }),
    new Table({ width: { size: total, type: WidthType.DXA }, columnWidths: widths,
      rows: [new TableRow({ tableHeader: true, children: headers.map((h, i) => cell(h, i, true)) }),
        ...rows.map((r) => new TableRow({ cantSplit: true, children: r.map((c, i) => cell(c, i, false)) }))] }),
    new Paragraph({ spacing: { after: 120 }, children: [] }),
  ];
}
function CODE(lines, caption) {
  const ps = lines.split("\n").map((l) => new Paragraph({
    shading: { fill: "F4F6F8", type: ShadingType.CLEAR }, spacing: { after: 0, line: 240 },
    children: [new TextRun({ text: l.replace(/\t/g, "    ") || " ", font: "Consolas", size: 16 })] }));
  return [new Paragraph({ keepNext: true, spacing: { before: 120, after: 40 },
    children: [new TextRun({ text: caption, bold: true, size: 18, color: "1F4E79" })] }), ...ps,
    new Paragraph({ spacing: { after: 120 }, children: [] })];
}
function NOTE(t) {
  return new Paragraph({ shading: { fill: "FFF4CC", type: ShadingType.CLEAR },
    border: { left: { style: BorderStyle.SINGLE, size: 18, color: "E0A800", space: 6 } },
    spacing: { before: 120, after: 160, line: 280 }, children: runs(t, { size: 19 }) });
}
// extrae un fragmento de código fuente entre dos marcadores
function SRC(file, startRe, endRe, maxLines = 60) {
  const lines = fs.readFileSync(path.join(ROOT, file), "utf8").split("\n");
  const s = lines.findIndex((l) => startRe.test(l));
  let e = lines.findIndex((l, i) => i > s && endRe.test(l));
  if (e < 0) e = s + maxLines;
  return lines.slice(s, Math.min(e + 1, s + maxLines)).join("\n");
}

// ---------- datos derivados ----------
const stP = R.limpieza[0], stF = R.limpieza[1];
const descTot = (s) => s.Leidas - s.Validas;
const totPos = E.Total[0], totFal = E.Total[1];
const letal = (100 * totFal) / totPos;
const bc = R.bench_carga, bt = R.bench_train;
const seq = bc[0];
const best = bc.slice(1).reduce((a, b) => (b.segundos < a.segundos ? b : a));
const t1 = bt[0], t2 = bt.find((b) => b.workers === 2) || bt[1];
const GR = ["0-17", "18-29", "30-39", "40-49", "50-59", "60-69", "70-79", "80+"];
const OLAS = ["Ola 1 (mar–nov 2020)", "Ola 2 (dic 2020–oct 2021)", "Ola 3 Ómicron (nov 2021–abr 2022)", "Post-olas (may 2022+)"];
const DEPTS = ["AMAZONAS", "ANCASH", "APURIMAC", "AREQUIPA", "AYACUCHO", "CAJAMARCA", "CALLAO", "CUSCO", "HUANCAVELICA", "HUANUCO", "ICA", "JUNIN", "LA LIBERTAD", "LAMBAYEQUE", "LIMA", "LORETO", "MADRE DE DIOS", "MOQUEGUA", "PASCO", "PIURA", "PUNO", "SAN MARTIN", "TACNA", "TUMBES", "UCAYALI"];
const SINT = "**Datos reales:** corrida sobre los CSV del MINSA (positivos, corte 03/12/2024; fallecidos, corte 17/03/2024) en una máquina Linux de " + R.cpus + " núcleos con " + R.go_version + ". Salida completa en results/ejecucion.log y results/resultados.json.";
const LINK_REPO = "https://github.com/UPC-CC65-202620/TF-Caso1-Epidemiologia-Go";
const PREG = "¿En qué provincias se concentrará el mayor número de casos graves (fallecimientos) según la edad, el sexo y el momento epidemiológico de sus casos positivos recientes, para priorizar la asignación de camas UCI, oxígeno medicinal y campañas de vacunación de refuerzo?";

// ---------- carátula ----------
const cover = [
  new Paragraph({ alignment: AlignmentType.CENTER, spacing: { before: 600, after: 240 },
    children: [new ImageRun({ type: "png", data: fs.readFileSync(path.join(__dirname, "logo_upc.png")), transformation: { width: 110, height: 111 } })] }),
  ...["UNIVERSIDAD PERUANA DE CIENCIAS APLICADAS", "FACULTAD DE INGENIERÍA", "CIENCIAS DE LA COMPUTACIÓN"].map((t) =>
    new Paragraph({ alignment: AlignmentType.CENTER, spacing: { after: 80 }, children: [new TextRun({ text: t, bold: true, size: 26 })] })),
  new Paragraph({ alignment: AlignmentType.CENTER, spacing: { before: 400, after: 80 }, children: [new TextRun({ text: "CURSO: 1ACC0065 – CC65 PROGRAMACIÓN CONCURRENTE Y DISTRIBUIDA", size: 22 })] }),
  new Paragraph({ alignment: AlignmentType.CENTER, spacing: { after: 400 }, children: [new TextRun({ text: "ENTREGABLE 1 (PC3) – TRABAJO FINAL", bold: true, size: 22 })] }),
  new Paragraph({ alignment: AlignmentType.CENTER, spacing: { after: 80 }, children: [new TextRun({ text: "CASO 1: PREDICCIÓN DE RIESGO EPIDEMIOLÓGICO NACIONAL (COVID-19)", bold: true, size: 28, color: "1F4E79" })] }),
  new Paragraph({ alignment: AlignmentType.CENTER, spacing: { after: 600 }, children: [new TextRun({ text: "Regresión logística distribuida y paralelismo en Go sobre datos abiertos del MINSA", size: 24, color: "1F4E79" })] }),
  P("**DOCENTE:** ==[completar]==", { alignment: AlignmentType.LEFT, indent: { left: 1800 } }),
  P("**NRC / SECCIÓN:** ==[completar]==", { alignment: AlignmentType.LEFT, indent: { left: 1800 } }),
  P("**INTEGRANTES:**", { alignment: AlignmentType.LEFT, indent: { left: 1800 } }),
  P("Rojas Cuadros, Fabian Marcelo (U202218498)", { alignment: AlignmentType.LEFT, indent: { left: 2300 } }),
  P("Ballón Villar, Diego Eduardo (U201520327)", { alignment: AlignmentType.LEFT, indent: { left: 2300 } }),
  new Paragraph({ alignment: AlignmentType.CENTER, spacing: { before: 600 }, children: [new TextRun({ text: "Ciclo académico 2026-20 · Lima, noviembre de 2026", size: 22 })] }),
  PB(),
];

// ---------- cuerpo ----------
const body = [];
const add = (...xs) => xs.flat().forEach((x) => body.push(x));

add(H1("Resumen"),
  P(`El presente informe corresponde al Entregable 1 (PC3) del trabajo final del curso. Aborda el Caso 1: anticipar la severidad de los casos de COVID-19 en el Perú a partir de los conjuntos de datos abiertos del Ministerio de Salud (MINSA) “Casos positivos por COVID-19” y “Fallecidos por COVID-19”, publicados en la Plataforma Nacional de Datos Abiertos. Como ninguno de los dos datasets incluye comorbilidades ni hospitalización, se definió como evento de severidad el **fallecimiento** y como predictores la edad, el sexo, el departamento y el periodo epidemiológico (ola) del caso.`),
  P(`En esta etapa se implementaron en Go, sin librerías externas, las dos primeras partes de la arquitectura: (A) un **cargador concurrente** que divide el CSV en bloques de bytes y los procesa con un pool de goroutines comunicadas por canales con buffer, aplicando seis reglas de limpieza y anonimización; y (B) un **motor de entrenamiento paralelo** de regresión logística por descenso de gradiente con mini-lotes, organizado como Parameter Server con P workers, cuyos mensajes (pesos y gradientes) están diseñados para viajar por TCP entre contenedores en la PC4. Las pruebas automatizadas (incluido el detector de condiciones de carrera \`go test -race\`) pasan, la carga concurrente de ${fmt(stP.Leidas)} filas obtuvo un speedup de ${fmt(best.speedup, 2)}× y el entrenamiento ${fmt(t2.speedup, 2)}× con 2 workers sobre una máquina de ${R.cpus} núcleos, y el modelo alcanzó un AUC de ${fmt(M.AUC, 3)} en el conjunto de prueba.`),
  PB());

add(new Paragraph({ children: [new TextRun({ text: "Índice", bold: true, size: 32 })], spacing: { after: 200 } }),
  new TableOfContents("Índice", { hyperlink: true, headingStyleRange: "1-2" }),
  P("==(Si el índice aparece vacío: clic derecho → Actualizar campo → Actualizar toda la tabla.)=="),
  PB());

// 1. Problema
add(H1("1. Descripción del problema y motivación"),
  P("Durante las olas de COVID-19 el sistema de salud peruano llegó al límite de su capacidad de camas UCI y de oxígeno medicinal. El MINSA reportó más de 4.5 millones de casos positivos y alrededor de 221 mil fallecidos entre 2020 y 2024 (Ministerio de Salud [MINSA], 2026a, 2026b). Una parte importante de esas muertes se concentró en semanas y provincias donde la demanda de atención crítica superó la oferta, de modo que la asignación de recursos se hizo de forma reactiva."),
  P("La información necesaria para anticiparse existe: cada caso positivo registra edad, sexo, ubicación y fecha, y se sabe que el riesgo de muerte crece fuertemente con la edad. El cuello de botella es computacional y operativo: procesar millones de registros heterogéneos, limpiarlos y reentrenar un modelo cada vez que se publica un nuevo corte toma demasiado tiempo con un procesamiento secuencial. Este proyecto propone hacerlo con programación concurrente y distribuida en Go."),
  H2("1.1. Pregunta de impacto social"),
  NOTE(`**Pregunta que atiende el sistema:** ${PREG}`),
  P("Para responderla, el sistema estima para cada caso positivo la probabilidad de que termine en fallecimiento y suma esas probabilidades por provincia. El resultado es el número esperado de casos graves en cada provincia, que puede compararse con su capacidad instalada para decidir dónde reforzar camas y oxígeno antes de que la demanda se concrete."),
  H2("1.2. Planteamiento del problema de Machine Learning"),
  B("**Tipo de problema:** clasificación binaria (¿el caso termina en fallecimiento? sí/no)."),
  B("**Algoritmo asignado:** regresión logística distribuida (descenso de gradiente con Parameter Server)."),
  B("**Unidad de análisis:** un caso positivo o un fallecido registrado por el MINSA."),
  B("**Predictores:** edad, sexo, departamento y periodo epidemiológico. Las comorbilidades y la hospitalización **no están disponibles** en los datos abiertos, por lo que el alcance se ajustó a lo que los datos permiten responder."),
  B("**Alcance de la PC3:** Parte A (cargador concurrente) y Parte B (entrenamiento paralelo con goroutines). La distribución en contenedores vía TCP, la API, MongoDB/Redis y la SPA corresponden a la PC4 y al TB2."));

// 2. Objetivos
add(H1("2. Objetivos"),
  H2("2.1. Objetivo general"),
  P("Construir una aplicación concurrente y distribuida en Go, desplegada con Docker Compose, que procese más de 2.5 millones de registros de COVID-19 del MINSA y estime el riesgo de casos graves por provincia mediante regresión logística entrenada en paralelo."),
  H2("2.2. Objetivos específicos"),
  N("Implementar un cargador concurrente en Go (goroutines, canales con buffer y \`sync.WaitGroup\`) que limpie y valide los datos del MINSA sin condiciones de carrera. **(PC3 – logrado)**"),
  N("Diseñar e implementar la regresión logística con descenso de gradiente por mini-lotes en paralelo bajo el patrón Parameter Server. **(PC3 – logrado con goroutines; PC4 – nodos TCP)**"),
  N("Exponer el modelo mediante una API coordinadora en Go con latencia menor a 100 ms y persistencia en MongoDB y Redis. **(PC4)**"),
  N("Desarrollar una SPA que muestre en tiempo real las predicciones, las métricas del clúster y el impacto por provincia. **(TB2)**"));

// 3. Arquitectura
add(H1("3. Arquitectura de la solución"),
  P("La solución sigue la arquitectura de referencia del enunciado: un frontend SPA, una API coordinadora, un clúster de nodos ML y dos bases de datos, todo en contenedores Docker. La Tabla 1 resume qué se implementa en cada entregable."),
  ...TABLE(["Componente", "Responsabilidad", "Tecnología", "Entrega"], [
    ["Parte A – Cargador", "Leer, limpiar y anonimizar los CSV del MINSA en paralelo", "Go: goroutines, canales, WaitGroup", "PC3 ✔"],
    ["Parte B – Motor ML", "Entrenar la regresión logística en paralelo (Parameter Server)", "Go: goroutines + canales (PC3), sockets TCP (PC4)", "PC3 ✔ / PC4"],
    ["Parte C – API", "Coordinar el clúster, exponer predicciones REST y WebSocket", "Go net/http, Swagger, JWT", "PC4"],
    ["Bases de datos", "Historial y métricas (persistente) y caché de consultas", "MongoDB, Redis", "PC4"],
    ["Parte D – SPA", "Dashboard de impacto, simulador y panel del clúster", "React o Angular", "TB2"],
  ], [1900, 3300, 2526, 1300], "Componentes de la arquitectura y entregable en el que se implementan"),
  P("Las Figuras 1 y 2 muestran el diseño de las dos partes implementadas en esta entrega. El código está organizado en paquetes independientes (\`internal/data\` e \`internal/ml\`), lo que permitió que cada integrante avanzara en paralelo."),
  ...IMG("arq_cargador.png", 6.3, 2.5, "Parte A: cargador concurrente (worker pool con fan-in)."),
  ...IMG("arq_entrenamiento.png", 6.3, 2.8, "Parte B: entrenamiento paralelo con Parameter Server."));

// 4. Limpieza y análisis de datos
add(H1("4. Limpieza y análisis de datos"),
  H2("4.1. Fuentes de datos"),
  ...TABLE(["Dataset", "Columnas usadas", "Separador / fecha", "Volumen (último corte)"], [
    ["Casos positivos por COVID-19 (MINSA)", "DEPARTAMENTO, PROVINCIA, EDAD, SEXO, FECHA_RESULTADO", "; · AAAAMMDD", `${fmt(stP.Leidas)} filas (corte 03/12/2024)`],
    ["Fallecidos por COVID-19 (MINSA)", "DEPARTAMENTO, PROVINCIA, EDAD_DECLARADA, SEXO, FECHA_FALLECIMIENTO", "; · AAAAMMDD", `${fmt(stF.Leidas)} filas (corte 17/03/2024)`],
  ], [2400, 3226, 1600, 1800], "Datasets del Portal Nacional de Datos Abiertos utilizados"),
  P("Ambos datasets están publicados bajo licencia Open Data Commons Attribution y se actualizan desde el MINSA. El de positivos incluye además el método de diagnóstico (\`METODODX\`) y el identificador anonimizado \`id_persona\`, y el de fallecidos el criterio de clasificación de la defunción (\`CLASIFICACION_DEF\`) y un \`UUID\`. Estas columnas no se usan como predictores: el método de prueba no está disponible al momento de decidir sobre un paciente y los identificadores no aportan información clínica."),
  H2("4.2. Construcción de la variable objetivo"),
  P("Ningún dataset trae la severidad de cada caso, así que se unieron ambos archivos con un diseño **caso-base**: cada fila de fallecidos es un caso (y = 1) y cada fila de positivos es un control (y = 0). Como los controles representan a todos los casos positivos, las odds que estima la regresión logística, p/(1 − p), son directamente una estimación de la **letalidad** (fallecidos / positivos) de un caso con ese perfil (King y Zeng, 2001). Esa propiedad es la que permite sumar letalidades esperadas por provincia para responder la pregunta de impacto."),
  P("Se evaluó primero cruzar ambos archivos por persona para etiquetar cada caso positivo directamente, pero no es posible: en el corte descargado la columna `id_persona` de positivos está vacía en casi todos los registros (solo unos 12 mil valores distintos en 4.58 millones de filas) y apenas el 0.1% de los `UUID` de fallecidos coincide con ella. Por eso no se eliminan duplicados entre archivos; el diseño caso-base justamente supone que los controles incluyen a los casos."),
  H2("4.3. Procedimiento de limpieza"),
  P("La limpieza se aplica dentro de cada worker del cargador, en el mismo paso del parseo, para no recorrer los datos dos veces. Cada fila pasa por las siguientes reglas en orden; si falla una, se descarta y se cuenta el motivo:"),
  ...TABLE(["#", "Regla", "Acción"], [
    ["1", "Número de columnas distinto al del encabezado", "Descartar (fila corrupta)"],
    ["2", "Edad vacía o no numérica (se acepta “45.0”)", "Descartar"],
    ["3", "Edad mayor a 110 años", "Descartar (error de digitación)"],
    ["4", "Sexo vacío o distinto de MASCULINO/FEMENINO (o M/F)", "Descartar"],
    ["5", "Departamento vacío, “EN INVESTIGACIÓN” o fuera del catálogo de 25", "Descartar; se normalizan tildes y variantes de Lima"],
    ["6", "Fecha vacía, mal formada o fuera de 2020–2026 (acepta AAAAMMDD, AAAA-MM-DD, DD/MM/AAAA)", "Descartar"],
  ], [500, 5226, 3300], "Reglas de limpieza aplicadas por el cargador"),
  P("Además se aplican transformaciones: mayúsculas y recorte de espacios, eliminación de comillas y del BOM UTF-8, detección automática del separador, ubicación de columnas por nombre (el MINSA ha cambiado el orden entre cortes), derivación del mes (AAAAMM) y de la ola epidemiológica, y *interning* de los nombres de provincia para no duplicar millones de cadenas en memoria."),
  P("En el análisis previo de los archivos reales se encontraron, entre otros problemas: 394 fechas de resultado con año 1899, unas 450 edades vacías o imposibles, 2 filas de fallecidos desplazadas (el sexo aparece en la columna de departamento) y 236 654 casos positivos (5.2%) con provincia “EN INVESTIGACIÓN”. Estos últimos se conservan para entrenar, porque la provincia no es un predictor, pero se excluyen del ranking de impacto por provincia."),
  P("**Anonimización (Ley N.° 29733):** el cargador nunca lee \`id_persona\`, \`UUID\`, \`FECHA_NAC\` ni el ubigeo distrital. El registro en memoria (\`Record\`) solo contiene edad, sexo, departamento, provincia, mes, ola y etiqueta, por lo que no es posible reidentificar a una persona a partir de lo que procesa el sistema."),
  H2("4.4. Evidencias de la limpieza"),
  NOTE(SINT),
  ...TABLE(["Archivo", "Filas leídas", "Válidas", "Descartadas", "% descartado"], [
    ["Positivos", fmt(stP.Leidas), fmt(stP.Validas), fmt(descTot(stP)), fmt((100 * descTot(stP)) / stP.Leidas, 2) + "%"],
    ["Fallecidos", fmt(stF.Leidas), fmt(stF.Validas), fmt(descTot(stF)), fmt((100 * descTot(stF)) / stF.Leidas, 2) + "%"],
    ["**Total**", `**${fmt(stP.Leidas + stF.Leidas)}**`, `**${fmt(stP.Validas + stF.Validas)}**`, `**${fmt(descTot(stP) + descTot(stF))}**`, ""],
  ], [1826, 1800, 1800, 1800, 1800], "Resultado de la limpieza por archivo"),
  ...IMG("limpieza.png", 6.0, 2.4, "Registros descartados por regla de limpieza."),
  H2("4.5. Análisis exploratorio (EDA)"),
  P(`El EDA se calcula en paralelo con un esquema map-reduce (\`CalcularEDA\`): cada goroutine cuenta sobre un tramo del slice y luego se suman los parciales. El dataset unificado tiene ${fmt(totPos + totFal)} registros (${fmt(totPos)} positivos y ${fmt(totFal)} fallecidos), una letalidad global de ${fmt(letal, 2)}% y ${E.Provincias} provincias. La edad media es de ${fmt(E.EdadMedia[0], 1)} años en positivos y de ${fmt(E.EdadMedia[1], 1)} en fallecidos.`),
  ...IMG("eda_mensual.png", 6.3, 2.5, "Casos positivos y fallecidos por mes."),
  P("La serie mensual muestra las olas epidemiológicas. En la tercera ola (Ómicron) los casos fueron muchos más, pero los fallecidos fueron proporcionalmente pocos, por la vacunación y la menor virulencia de la variante. Por eso la ola entra al modelo como variable: el mismo perfil de paciente no tuvo el mismo riesgo en 2021 que en 2022."),
  ...IMG("eda_edad.png", 6.3, 2.25, "Distribución de casos por edad y letalidad por grupo de edad."),
  ...TABLE(["Grupo de edad", "Positivos", "Fallecidos", "Letalidad"], GR.map((g, i) =>
    [g, fmt(E.PorEdad[0][i]), fmt(E.PorEdad[1][i]), fmt(E.Letalidad[i], 2) + "%"]), [2600, 2142, 2142, 2142], "Letalidad por grupo de edad"),
  P("La edad es, por mucho, el predictor más fuerte: la letalidad crece de forma casi exponencial a partir de los 50 años. La única excepción es el grupo de 0 a 17 años, con una letalidad algo mayor que el de 18 a 29, explicada principalmente por los menores de un año. Eso justifica incluir la edad estandarizada y su cuadrado en el modelo. También se observan diferencias por sexo (letalidad de " + fmt(E.LetalSexo[1], 2) + "% en hombres frente a " + fmt(E.LetalSexo[0], 2) + "% en mujeres) y por departamento (Figura 6)."),
  ...TABLE(["Periodo", "Positivos", "Fallecidos", "Letalidad"], OLAS.map((o, i) =>
    [o, fmt(E.PorOla[0][i]), fmt(E.PorOla[1][i]), fmt(E.LetalOla[i], 2) + "%"]), [3400, 1875, 1875, 1876], "Letalidad por periodo epidemiológico"),
  ...IMG("eda_departamento.png", 5.0, 3.9, "Letalidad por departamento."),
  P("**Balance de clases:** los fallecidos son alrededor del " + fmt((100 * totFal) / (totPos + totFal), 1) + "% de los registros. Un modelo que siempre prediga “no fallece” acertaría el " + pct(M.AccuracyBaseline) + " de las veces sin aportar nada, así que se entrena con ponderación de clases y se evalúa con AUC, recall y precisión en lugar de solo exactitud."));

// 5. Diseño del modelo
add(H1("5. Diseño del modelo ML distribuido"),
  H2("5.1. Regresión logística"),
  P("Cada registro se codifica en un vector x de 32 características: la edad estandarizada z, su cuadrado z², el indicador de sexo masculino, 25 indicadores one-hot de departamento y 4 de periodo epidemiológico. Como solo 5 de las 32 posiciones son distintas de cero, el código usa una representación dispersa: calcular wᵀx cuesta 5 multiplicaciones y no 32. El modelo es:"),
  FORM("z = wᵀx + b        ŷ = σ(z) = 1 / (1 + e^(−z))"),
  P("Se minimiza la entropía cruzada ponderada con regularización L2, donde ωᵢ = n₀/n₁ si yᵢ = 1 (compensa el desbalance) y ωᵢ = 1 si yᵢ = 0:"),
  FORM("L(w,b) = −(1/Σωᵢ) Σ ωᵢ [ yᵢ log ŷᵢ + (1 − yᵢ) log(1 − ŷᵢ) ] + (λ/2)‖w‖²"),
  P("Su gradiente tiene una forma cerrada muy simple, que es lo que cada worker calcula sobre su mini-lote:"),
  FORM("∂L/∂w = (1/Σωᵢ) Σ ωᵢ (ŷᵢ − yᵢ) xᵢ + λw          ∂L/∂b = (1/Σωᵢ) Σ ωᵢ (ŷᵢ − yᵢ)"),
  P("La actualización es w ← w − η_t ∇w, con tasa de aprendizaje decreciente η_t = η₀ / (1 + 0.5·época). Para estimar letalidades se corrige el intercepto por la ponderación usada (b − ln ω), de modo que exp(z) recupere la escala original (King y Zeng, 2001)."),
  ...TABLE(["Hiperparámetro", "Valor", "Justificación"], [
    ["Mini-lote global B", fmt(R.config.LoteGlobal), "Suficiente trabajo por paso para amortizar la sincronización"],
    ["Workers P", String(R.config.Workers), "Mini-lote local B/P por worker"],
    ["Épocas", String(R.config.Epocas), "La pérdida se estabiliza antes de la 4.ª época (Figura 9)"],
    ["η₀ / decaimiento", `${R.config.TasaAprend} / ${R.config.Decaimiento}`, "Variables estandarizadas: tolera un η₀ alto al inicio"],
    ["λ (L2)", String(R.config.L2), "Evita pesos extremos en departamentos con pocos casos"],
    ["División train/test", "80% / 20% aleatoria (semilla fija)", "Evaluación reproducible"],
  ], [2400, 2300, 4326], "Hiperparámetros del entrenamiento"),
  H2("5.2. Distribución: Parameter Server con SGD síncrono"),
  P("El entrenamiento sigue el patrón Parameter Server (Li et al., 2014) en su variante síncrona:"),
  N("El coordinador baraja el conjunto de entrenamiento y lo reparte en P fragmentos (shards), uno por worker.", "num2"),
  N("En cada paso envía (broadcast) los pesos actuales w y b a los P workers.", "num2"),
  N("Cada worker toma el siguiente mini-lote de B/P registros de su shard y calcula el gradiente local y la pérdida, en paralelo con los demás.", "num2"),
  N("El coordinador espera los P gradientes (barrera), los suma, los normaliza por el peso total y actualiza los pesos. Luego vuelve al paso 2.", "num2"),
  P("Como el mini-lote global B se mantiene constante, el resultado matemático no depende de P: con 1, 2, 4 u 8 workers se hacen los mismos pasos sobre la misma cantidad de datos, solo que repartidos. Eso explica que la pérdida final y el AUC coincidan en la Tabla 12 y permite comparar tiempos de forma justa. Se eligió la variante síncrona sobre la asíncrona porque garantiza convergencia determinista y una sola versión de los pesos, a costa de esperar al worker más lento en cada paso."),
  P("**Protocolo coordinador-worker.** La comunicación se reduce a dos mensajes, \`MsgPesos\` (época, paso, w, b, fin) y \`MsgGradiente\` (worker, paso, ∇w, ∇b, pérdida, peso total, muestras). En la PC3 viajan por canales Go; en la PC4 se serializarán por sockets TCP sin cambiar la lógica, y cada worker será un contenedor Docker con su propio shard."));

// 6. Paralelización
add(H1("6. Paralelización del cálculo en Go"),
  H2("6.1. Justificación"),
  P("Las dos etapas costosas del pipeline son embarazosamente paralelas por datos: cada fila del CSV se puede parsear sin mirar a las demás, y el gradiente de la regresión logística es una suma sobre registros, que se puede partir en sumas parciales independientes y combinar al final. Esto hace que la descomposición por datos (data parallelism) sea la elección natural, con comunicación solo al final de cada bloque o de cada paso."),
  H2("6.2. Técnicas y patrones utilizados"),
  ...TABLE(["Patrón / técnica", "Dónde se usa", "Para qué"], [
    ["Worker pool", "CargarConcurrente", "W goroutines consumen una cola de bloques; se crean 4W bloques para balancear la carga"],
    ["Partición por rangos de bytes", "partir / procesarBloque", "Cada worker abre el archivo y hace Seek a su bloque: lectura en paralelo sin un lector central"],
    ["Fan-in con canal buffered", "chan Lote", "Los workers envían lotes de 8 192 registros a un único colector; el buffer desacopla productores y consumidor"],
    ["sync.WaitGroup + cierre de canal", "CargarConcurrente", "Una goroutine espera a todos los workers y cierra el canal, lo que termina el bucle del colector"],
    ["Estado local por goroutine", "Parser, Stats por worker", "Cada worker tiene su propio buffer, mapa de interning y contadores; se suman al final, sin locks"],
    ["Map-reduce", "CalcularEDA, PredecirParalelo", "Conteos y predicciones por tramos disjuntos del slice, luego reducción"],
    ["Parameter Server + barrera", "EntrenarParalelo", "Broadcast de pesos y reducción de gradientes; solo el coordinador escribe los pesos"],
    ["sync/atomic", "contador de progreso", "Contador de filas procesadas compartido sin mutex"],
  ], [2300, 2300, 4426], "Patrones de concurrencia aplicados"),
  H2("6.3. Problema de las líneas cortadas entre bloques"),
  P("Al dividir el archivo por bytes, el borde de un bloque cae casi siempre en medio de una línea. La regla aplicada es que **una línea pertenece al bloque donde empieza**: cada worker (salvo el primero) retrocede un byte y descarta todo hasta el primer salto de línea, y sigue leyendo mientras la línea empiece antes del final de su bloque, aunque termine después. Así ninguna línea se pierde ni se procesa dos veces. La prueba \`TestConcurrenteIgualSecuencial\` lo verifica con 1 a 64 workers, saltos de línea Unix y Windows, y archivos sin salto final."),
  H2("6.4. Ausencia de condiciones de carrera"),
  P("En ningún punto dos goroutines escriben la misma variable: (1) cada worker del cargador escribe solo en su posición de \`statsW[id]\`; (2) el slice de resultados lo modifica únicamente el colector; (3) en el entrenamiento cada worker recibe su propia copia de los pesos y baraja solo su shard, y el coordinador es la única goroutine que escribe w y b; (4) en la predicción paralela cada goroutine escribe un rango disjunto del slice de salida. La sincronización se hace con canales y no con mutex, siguiendo el principio de Go de “compartir memoria comunicando”. Todas las pruebas se ejecutan con \`go test -race\` sin advertencias."));

// 7. Evidencias
const codeBloque = SRC("internal/data/loader.go", /^func procesarBloque/, /^\tp := NuevoParser/);
const codePool = SRC("internal/data/loader.go", /bloques := partir\(primerByte/, /^\t\trecs = append\(recs, lote.Recs/);
const codeGrad = SRC("internal/ml/logreg.go", /^func gradienteLote/, /^}/);
const codeTrain = SRC("internal/ml/train.go", /^\tfor ep := 0; ep < cfg.Epocas/, /^\t\tres.Historial = append/);
add(H1("7. Evidencias de la implementación"),
  H2("7.1. Estructura del código"),
  P(`El proyecto está escrito íntegramente en Go 1.24 usando solo la biblioteca estándar (${"≈"}2 000 líneas, incluyendo pruebas). Repositorio: ${LINK_REPO}`),
  ...TABLE(["Ruta", "Contenido"], [
    ["internal/data/record.go", "Struct Record, catálogo de departamentos, olas epidemiológicas"],
    ["internal/data/parse.go", "Esquema por nombre de columna, parser sin asignaciones, reglas de limpieza"],
    ["internal/data/loader.go", "Cargador concurrente (worker pool + fan-in) y cargador secuencial de referencia"],
    ["internal/data/eda.go", "Análisis exploratorio en paralelo (map-reduce)"],
    ["internal/ml/logreg.go", "Modelo, sigmoide, gradiente de la entropía cruzada ponderada"],
    ["internal/ml/train.go", "Parameter Server: coordinador, workers, protocolo de mensajes"],
    ["internal/ml/eval.go", "Métricas (matriz de confusión, AUC) y análisis de impacto por provincia"],
    ["cmd/pc3/main.go", "Pipeline completo y benchmarks"],
    ["cmd/gensample/main.go", "Generador de muestra sintética con el esquema MINSA (pruebas)"],
    ["*_test.go", "Pruebas unitarias, de equivalencia, de gradiente numérico y benchmarks"],
  ], [3000, 6026], "Organización del código fuente"),
  H2("7.2. Fragmentos de código relevantes"),
  ...CODE(codePool, "Código 1. Worker pool, canal con buffer y fan-in (internal/data/loader.go)"),
  ...CODE(codeBloque, "Código 2. Tratamiento de los bordes de bloque (internal/data/loader.go)"),
  ...CODE(codeGrad, "Código 3. Gradiente de la entropía cruzada ponderada (internal/ml/logreg.go)"),
  ...CODE(codeTrain, "Código 4. Bucle del Parameter Server: broadcast, barrera y actualización (internal/ml/train.go)"),
  H2("7.3. Pruebas automatizadas"),
  ...TABLE(["Prueba", "Qué verifica", "Resultado"], [
    ["TestParseLimpieza", "Cada regla de limpieza descarta lo que debe y el registro válido se parsea bien", "PASS"],
    ["TestEsquemaFallecidos", "Columnas del archivo de fallecidos y etiqueta y = 1", "PASS"],
    ["TestConcurrenteIgualSecuencial", "Mismo resultado que el cargador secuencial con 1–64 workers, LF/CRLF y sin salto final", "PASS"],
    ["TestEDA", "Conteos y letalidad por grupo", "PASS"],
    ["TestGradienteNumerico", "Gradiente analítico = diferencias finitas", "PASS"],
    ["TestParaleloConverge", "AUC > 0.85 y mismo resultado con 1, 2 y 4 workers", "PASS"],
    ["TestLetalidadEsperadaCalibrada", "La corrección del intercepto recupera el total observado (±15%)", "PASS"],
  ], [3000, 4826, 1200], "Pruebas ejecutadas con go test -race ./..."),
  ...CODE(fs.readFileSync(path.join(ROOT, "results/tests_race.log"), "utf8").trim(), "Salida de `go test -race -v ./...`"),
  H2("7.4. Rendimiento de la carga concurrente"),
  NOTE(SINT),
  ...TABLE(["Modo", "Workers", "Tiempo (s)", "Filas / s", "Speedup"], bc.map((b) =>
    [b.modo, String(b.workers), fmt(b.segundos, 3), fmt(b.filas_por_seg), fmt(b.speedup, 2) + "×"]),
    [2026, 1500, 1800, 2000, 1700], `Carga y limpieza de ${fmt(stP.Leidas)} filas (mediana de 3 corridas, ${R.cpus} núcleos)`),
  ...IMG("bench_carga.png", 5.6, 2.4, "Tiempo de carga secuencial frente a concurrente."),
  P(`Con 2 workers el tiempo baja de ${fmt(seq.segundos, 2)} s a ${fmt(bc.find((b) => b.workers === 2).segundos, 2)} s, y el mejor caso llega a ${fmt(best.speedup, 2)}× con ${best.workers} workers. Que el speedup supere ligeramente el número de núcleos (${R.cpus}) se explica porque la versión concurrente solapa la lectura de disco con el parseo, mientras la secuencial espera la E/S y además hace crecer su slice sin capacidad reservada. Entre 2, 4 y 8 workers las diferencias son pequeñas: las goroutines extra se reparten los mismos dos núcleos. En máquinas con más núcleos el speedup debería seguir creciendo hasta saturar el disco.`),
  H2("7.5. Rendimiento y resultados del entrenamiento"),
  NOTE(SINT),
  ...TABLE(["Workers", "Tiempo (s)", "Speedup", "Eficiencia", "Pérdida final", "AUC test"], bt.map((b) =>
    [String(b.workers), fmt(b.segundos, 3), fmt(b.speedup, 2) + "×", fmt(100 * b.eficiencia, 0) + "%", fmt(b.perdida_final, 4), fmt(b.auc, 4)]),
    [1300, 1500, 1500, 1500, 1600, 1626], `Entrenamiento de ${R.config.Epocas} épocas con mini-lote global constante`),
  ...IMG("bench_train.png", 6.3, 2.1, "Tiempo y speedup del entrenamiento paralelo."),
  ...IMG("perdida.png", 4.0, 2.4, "Convergencia de la pérdida por época (4 workers)."),
  ...TABLE(["Métrica (test, umbral 0.5)", "Valor"], [
    ["AUC-ROC", fmt(M.AUC, 4)],
    ["Recall (sensibilidad)", pct(M.Recall)],
    ["Especificidad", pct(M.Especificidad)],
    ["Precisión", pct(M.Precision)],
    ["F1", fmt(M.F1, 3)],
    ["Exactitud (accuracy)", pct(M.Accuracy)],
    ["Exactitud de la línea base (siempre “no fallece”)", pct(M.AccuracyBaseline)],
    ["Matriz de confusión", `TP ${fmt(M.TP)} · FP ${fmt(M.FP)} · TN ${fmt(M.TN)} · FN ${fmt(M.FN)}`],
  ], [5000, 4026], `Métricas sobre ${fmt(M.N)} registros de prueba`),
  P(`El modelo distingue bien entre casos que fallecen y casos que no (AUC ${fmt(M.AUC, 3)}) y detecta al ${pct(M.Recall)} de los fallecidos. Su exactitud es menor que la de la línea base porque la ponderación de clases prioriza no dejar pasar casos graves a cambio de más falsos positivos; para triage esa es la decisión correcta, porque un falso negativo cuesta una vida y un falso positivo, una vigilancia adicional. El umbral de decisión podrá ajustarse en la SPA según la capacidad disponible.`),
  H2("7.6. Respuesta preliminar a la pregunta de impacto"),
  NOTE(SINT),
  P(`Con el modelo entrenado se estimó la letalidad esperada de cada caso positivo del último brote registrado (diciembre de 2023 a marzo de 2024, ${fmt(53361)} casos) y se sumó por provincia. Se excluyeron ${fmt(R.impacto_excluidos)} casos sin provincia asignada. La Figura 10 muestra las 10 provincias con más casos graves esperados: allí debería priorizarse el refuerzo de camas UCI y oxígeno.`),
  ...IMG("impacto.png", 6.0, 3.1, "Provincias con mayor número de casos graves esperados."),
  ...TABLE(["#", "Provincia (departamento)", "Positivos", "Graves esperados", "Por 1 000 casos", "% 60+ años"], R.impacto_top.slice(0, 10).map((r, i) =>
    [String(i + 1), `${r.Provincia} (${r.Departamento})`, fmt(r.Positivos), fmt(r.GravesEsperados, 1), fmt(r.TasaEsperada, 1), fmt(r.Edad60Mas, 1) + "%"]),
    [500, 3026, 1300, 1500, 1400, 1300], "Ranking de provincias por casos graves esperados"),
  P(`**Validación de la escala.** En esa ventana el modelo esperaba ${fmt(R.impacto_departamentos.reduce((a, d) => a + d.GravesEsperados, 0), 0)} fallecimientos en las provincias asignadas y se registraron 351 en total. El orden de las provincias es lo útil para priorizar, pero la cifra absoluta está sobreestimada porque el periodo “post-olas” agrupa desde mayo de 2022 hasta marzo de 2024 y no capta la caída de la letalidad en 2023–2024. En la PC4 se reemplazará por una variable temporal más fina (trimestre) y se recalibrará el intercepto con los datos más recientes.`),
  P("La tasa por 1 000 casos permite además distinguir dos situaciones: provincias con muchos casos graves por volumen (Lima) y provincias con pocos casos pero una población de pacientes más envejecida, donde cada contagio pesa más. En la PC4 este cálculo se expondrá como endpoint de la API y en el TB2 como mapa en la SPA."));

// 8. Gestión del proyecto
add(H1("8. Gestión del proyecto: GitHub, Git Flow y Kanban"),
  P(`El código se versiona en un repositorio público de GitHub siguiendo Git Flow: \`main\` contiene solo versiones entregadas (etiqueta \`v0.1-pc3\`), \`develop\` integra el trabajo en curso y cada parte se desarrolla en una rama \`feature/*\` que se integra mediante pull request revisado por el otro integrante. El avance se registra en un tablero Kanban de GitHub Projects con las columnas Backlog, To do, In progress, Review y Done.`),
  ...TABLE(["Rama", "Contenido", "Responsable"], [
    ["feature/cargador-concurrente", "Esquema, parser, limpieza, cargador, pruebas de equivalencia", "Rojas Cuadros"],
    ["feature/eda", "Análisis exploratorio paralelo y gráficos", "Rojas Cuadros"],
    ["feature/regresion-paralela", "Modelo, gradiente, Parameter Server, métricas", "Ballón Villar"],
    ["feature/impacto-provincias", "Letalidad esperada y ranking de provincias", "Ballón Villar"],
    ["release/pc3", "Informe, benchmarks, README, Dockerfile", "Ambos"],
  ], [3200, 4126, 1700], "Ramas de Git Flow de la PC3"),
  NOTE("==Insertar aquí capturas de: (1) el gráfico de ramas (Insights → Network o `git log --graph --oneline --all`), (2) los pull requests cerrados y (3) el tablero Kanban con las tarjetas de la PC3 en Done.=="));

// 9. ABET SO4
add(H1("9. Responsabilidad ética y profesional (ABET – Student Outcome 4)"),
  H2("9.1. Ética en el tratamiento de datos de salud"),
  P("Aunque los datos son abiertos, se refieren a personas enfermas y fallecidas. Por eso el sistema aplica minimización de datos: solo carga las cinco columnas necesarias, descarta todos los identificadores en el mismo momento de la lectura y trabaja a nivel de provincia, no de distrito ni de persona, en línea con la Ley N.° 29733. Además, el informe declara con transparencia las limitaciones del modelo: no usa comorbilidades porque no existen en los datos, y la sobreestimación de la escala absoluta en el último brote se reporta explícitamente."),
  H2("9.2. Sesgos y uso responsable"),
  P("El modelo incluye el departamento como variable. Un peso alto para un departamento puede reflejar menor acceso a atención o subregistro de casos leves, no un mayor riesgo biológico. Por eso sus resultados se presentan como apoyo a la **asignación de recursos hacia** las provincias de mayor riesgo, nunca para negar atención a un paciente individual. Esta advertencia se mostrará también en la SPA."),
  H2("9.3. Juicios informados sobre el impacto"),
  B("**Social:** anticipar dónde se concentrarán los casos graves permite redistribuir camas UCI, oxígeno y personal antes de que la demanda supere la oferta, y priorizar la vacunación de refuerzo en la población de 60 años a más."),
  B("**Económico:** el modelo se reentrena en segundos con hardware común, sin licencias, lo que hace viable su uso en direcciones regionales de salud con presupuesto limitado."),
  B("**Global y ambiental:** el diseño es reutilizable para otras epidemias con datos abiertos similares (por ejemplo, dengue) y Go permite procesar millones de registros con poco consumo de memoria (≈" + fmt(R.memoria_heap_mb, 0) + " MB para todo el dataset) y de energía."));

// 10-11
add(H1("10. Conclusiones"),
  N(`Se implementó en Go un cargador concurrente que procesa ${fmt(stP.Leidas + stF.Leidas)} filas con un patrón worker pool + fan-in y obtiene un speedup de ${fmt(best.speedup, 2)}× sobre la versión secuencial en ${R.cpus} núcleos, con resultados idénticos a ella verificados por pruebas automatizadas.`, "num3"),
  N("La regresión logística se entrena en paralelo bajo el patrón Parameter Server síncrono. El diseño evita condiciones de carrera por construcción (solo el coordinador escribe los pesos) y así lo confirma `go test -race`. El resultado del modelo no depende del número de workers, solo el tiempo.", "num3"),
  N(`Los datos abiertos del MINSA no contienen comorbilidades ni hospitalización, y sus identificadores no permiten cruzar casos con fallecidos. Se reformuló el problema como predicción de fallecimiento con un diseño caso-base que permite estimar letalidades. Con edad, sexo, departamento y ola el modelo alcanza un AUC de ${fmt(M.AUC, 3)}.`, "num3"),
  N("El sistema ya responde, de forma preliminar, la pregunta de impacto social: identifica las provincias con más casos graves esperados. Los mensajes entre coordinador y workers quedan definidos para llevar este entrenamiento a nodos TCP en contenedores en la PC4.", "num3"));
add(H1("11. Recomendaciones"),
  N("Recalibrar la escala absoluta del modelo con una variable temporal más fina (trimestre) para que el total de casos graves esperados coincida con los fallecimientos observados en los brotes recientes.", "num4"),
  N("Ejecutar los benchmarks en máquinas con más núcleos y en el clúster Docker de la PC4 para medir la escalabilidad real con 4 y 8 nodos.", "num4"),
  N("Implementar tolerancia a fallos en los workers TCP (heartbeat y reasignación de shards) para que la barrera síncrona no se bloquee si un nodo cae.", "num4"),
  N("Enriquecer el modelo con datos de vacunación por provincia (HIS-MINSA) y con la capacidad de camas UCI (SUSALUD) para pasar de casos graves esperados a déficit esperado de camas.", "num4"),
  N("Extender el mismo pipeline al dataset de dengue para cumplir el alcance completo del Caso 1.", "num4"));

// 12. Bibliografía
const bib = [
  "Bottou, L. (2010). Large-scale machine learning with stochastic gradient descent. En Y. Lechevallier y G. Saporta (Eds.), *Proceedings of COMPSTAT'2010* (pp. 177–186). Physica-Verlag. https://doi.org/10.1007/978-3-7908-2604-3_16",
  "Congreso de la República del Perú. (2011). *Ley N.° 29733, Ley de Protección de Datos Personales*. Diario Oficial El Peruano.",
  "Dean, J., Corrado, G., Monga, R., Chen, K., Devin, M., Le, Q. V., Mao, M. Z., Ranzato, M., Senior, A., Tucker, P., Yang, K., y Ng, A. Y. (2012). Large scale distributed deep networks. *Advances in Neural Information Processing Systems, 25*, 1223–1231.",
  "Donovan, A. A. A., y Kernighan, B. W. (2015). *The Go programming language*. Addison-Wesley Professional.",
  "Hosmer, D. W., Lemeshow, S., y Sturdivant, R. X. (2013). *Applied logistic regression* (3.ª ed.). Wiley. https://doi.org/10.1002/9781118548387",
  "King, G., y Zeng, L. (2001). Logistic regression in rare events data. *Political Analysis, 9*(2), 137–163. https://doi.org/10.1093/oxfordjournals.pan.a004868",
  "Li, M., Andersen, D. G., Park, J. W., Smola, A. J., Ahmed, A., Josifovski, V., Long, J., Shekita, E. J., y Su, B.-Y. (2014). Scaling distributed machine learning with the parameter server. En *11th USENIX Symposium on Operating Systems Design and Implementation (OSDI 14)* (pp. 583–598). USENIX Association.",
  "Ministerio de Salud del Perú. (2026a). *Casos positivos por COVID-19 – [Ministerio de Salud – MINSA]* [Conjunto de datos]. Plataforma Nacional de Datos Abiertos. https://www.datosabiertos.gob.pe/dataset/casos-positivos-por-covid-19-ministerio-de-salud-minsa",
  "Ministerio de Salud del Perú. (2026b). *Fallecidos por COVID-19 – [Ministerio de Salud – MINSA]* [Conjunto de datos]. Plataforma Nacional de Datos Abiertos. https://www.datosabiertos.gob.pe/dataset/fallecidos-por-covid-19-ministerio-de-salud-minsa",
  "Tanenbaum, A. S., y van Steen, M. (2017). *Distributed systems* (3.ª ed.). Maarten van Steen.",
];
add(H1("12. Bibliografía"));
bib.forEach((b) => {
  const parts = b.split(/(\*[^*]+\*)/);
  body.push(new Paragraph({ indent: { left: 720, hanging: 720 }, spacing: { after: 120, line: 280 },
    children: parts.map((t) => t.startsWith("*") ? new TextRun({ text: t.slice(1, -1), italics: true }) : new TextRun(t)) }));
});

// 13. Anexos
add(H1("13. Anexos"),
  H2("13.1. Enlaces"),
  B(`Repositorio GitHub: ${LINK_REPO} ==[verificar que sea público]==`),
  B("Tablero Kanban: ==[pegar enlace de GitHub Projects]=="),
  B("Video: no se requiere en la PC3 (se entrega con el TB2)."),
  B("Código fuente y resultados: archivo 1ACC0065_PC3_UXXXXXXXXX.zip adjunto."),
  H2("13.2. Cómo reproducir los resultados"),
  ...CODE(["# 1. Descargar los CSV del MINSA en data/ (positivos_covid.csv y fallecidos_covid.csv)",
    "go test -race ./...                              # pruebas",
    "go run ./cmd/pc3 -workers 4 -bench 1,2,4,8       # pipeline + benchmarks",
    "python3 scripts/graficos.py results/resultados.json results/figs",
    "docker build -t tf-caso1-pc3 . && docker run -v $PWD/data:/app/data -v $PWD/results:/app/results tf-caso1-pc3"].join("\n"), "Comandos"),
  H2("13.3. Reporte de participación"),
  NOTE("==Confirmar la distribución de tareas y porcentajes con el equipo antes de entregar.=="),
  ...TABLE(["Integrante", "Código", "Tareas realizadas en la PC3", "Participación"], [
    ["Rojas Cuadros, Fabian Marcelo", "U202218498", "Descarga y análisis de los datasets; esquema y reglas de limpieza; cargador concurrente y sus pruebas; EDA y gráficos; secciones 4 y 6 del informe", "50%"],
    ["Ballón Villar, Diego Eduardo", "U201520327", "Diseño del modelo; gradiente y Parameter Server; evaluación y métricas; análisis de impacto; benchmarks; secciones 5, 7 y 9 del informe; tablero Kanban", "50%"],
  ], [2400, 1300, 4126, 1200], "Reporte de participación – PC3"),
  H2("13.4. Pesos aprendidos por el modelo"),
  ...TABLE(["Característica", "Peso w"], [["Intercepto b", fmt(R.sesgo, 4)],
    ...["edad_z", "edad_z^2", "sexo_masculino"].map((k) => [k, fmt(R.pesos[k], 4)]),
    ...Object.keys(R.pesos).filter((k) => k.startsWith("Ola") || k.startsWith("Post") || k.startsWith("Ola 3")).map((k) => [k, fmt(R.pesos[k], 4)]),
  ], [5000, 4026], "Pesos principales (los 25 pesos departamentales están en results/resultados.json)"),
  P("La edad estandarizada tiene el peso positivo más grande: un caso una desviación estándar mayor en edad multiplica sus odds de fallecer por e^w ≈ " + fmt(Math.exp(R.pesos["edad_z"]), 1) + ". El sexo masculino las multiplica por ≈ " + fmt(Math.exp(R.pesos["sexo_masculino"]), 2) + ", y los periodos de Ómicron y post-olas tienen pesos negativos, coherentes con la vacunación."));

// ---------- documento ----------
const doc = new Document({
  creator: "Rojas Cuadros, F. M.; Ballón Villar, D. E.",
  title: "Informe PC3 – Caso 1 – CC65",
  styles: {
    default: { document: { run: { font: FONT, size: 21 } } },
    paragraphStyles: [
      { id: "Heading1", name: "Heading 1", basedOn: "Normal", next: "Normal", quickFormat: true,
        run: { font: FONT, size: 30, bold: true, color: "1F4E79" }, paragraph: { spacing: { before: 360, after: 160 }, keepNext: true, outlineLevel: 0 } },
      { id: "Heading2", name: "Heading 2", basedOn: "Normal", next: "Normal", quickFormat: true,
        run: { font: FONT, size: 24, bold: true, color: "2E75B6" }, paragraph: { spacing: { before: 240, after: 120 }, keepNext: true, outlineLevel: 1 } },
      { id: "Heading3", name: "Heading 3", basedOn: "Normal", next: "Normal", quickFormat: true,
        run: { font: FONT, size: 22, bold: true }, paragraph: { spacing: { before: 180, after: 100 }, keepNext: true, outlineLevel: 2 } },
    ],
  },
  numbering: { config: [
    { reference: "bul", levels: [{ level: 0, format: LevelFormat.BULLET, text: "•", alignment: AlignmentType.LEFT, style: { paragraph: { indent: { left: 720, hanging: 360 } } } }] },
    ...["num", "num2", "num3", "num4"].map((r) => ({ reference: r, levels: [{ level: 0, format: LevelFormat.DECIMAL, text: "%1.", alignment: AlignmentType.LEFT, style: { paragraph: { indent: { left: 720, hanging: 360 } } } }] })),
  ] },
  sections: [
    { properties: { page: { size: { width: 11906, height: 16838 }, margin: { top: 1440, bottom: 1440, left: 1440, right: 1440 } } }, children: cover },
    { properties: { page: { size: { width: 11906, height: 16838 }, margin: { top: 1440, bottom: 1440, left: 1440, right: 1440 } } },
      footers: { default: new Footer({ children: [new Paragraph({ alignment: AlignmentType.RIGHT,
        children: [new TextRun({ text: "CC65 · PC3 · Caso 1 · Página ", size: 16, color: "777777" }), new TextRun({ children: [PageNumber.CURRENT], size: 16, color: "777777" })] })] }) },
      children: body },
  ],
});

const out = path.join(__dirname, process.argv[2] || "1ACC0065_PC3_202620_Informe_U201520327.docx");
Packer.toBuffer(doc).then((b) => { fs.writeFileSync(out, b); console.log("ok", out); });
