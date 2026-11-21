package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Mongo agrupa el acceso a la base persistente.
type Mongo struct {
	cli *mongo.Client
	db  *mongo.Database
}

// ErrNoEncontrado se devuelve cuando un documento no existe.
var ErrNoEncontrado = errors.New("no encontrado")

// ConectarMongo abre el pool de conexiones y reintenta hasta que el servidor
// responda o venza el plazo (en Docker Compose Mongo puede tardar en subir).
func ConectarMongo(ctx context.Context, uri, base string, plazo time.Duration) (*Mongo, error) {
	cli, err := mongo.Connect(options.Client().ApplyURI(uri).
		SetMaxPoolSize(50).SetServerSelectionTimeout(3 * time.Second))
	if err != nil {
		return nil, err
	}
	limite := time.Now().Add(plazo)
	for {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = cli.Ping(c, nil)
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(limite) {
			return nil, fmt.Errorf("mongo no responde en %s: %w", uri, err)
		}
		time.Sleep(time.Second)
	}
	return &Mongo{cli: cli, db: cli.Database(base)}, nil
}

// Cerrar libera el pool.
func (m *Mongo) Cerrar(ctx context.Context) error { return m.cli.Disconnect(ctx) }

// Ping verifica la conexión (health check).
func (m *Mongo) Ping(ctx context.Context) error { return m.cli.Ping(ctx, nil) }

func (m *Mongo) col(n string) *mongo.Collection { return m.db.Collection(n) }

// CrearIndices define los índices de cada colección (idempotente).
func (m *Mongo) CrearIndices(ctx context.Context) error {
	idx := map[string][]mongo.IndexModel{
		ColUsuarios: {{Keys: bson.D{{Key: "username", Value: 1}}, Options: options.Index().SetUnique(true)}},
		ColModelos:  {{Keys: bson.D{{Key: "activo", Value: 1}}}, {Keys: bson.D{{Key: "creado", Value: -1}}}},
		ColEntrenamientos: {
			{Keys: bson.D{{Key: "creado", Value: -1}}},
			{Keys: bson.D{{Key: "estado", Value: 1}}},
		},
		ColPredicciones: {
			{Keys: bson.D{{Key: "ts", Value: -1}}},
			{Keys: bson.D{{Key: "entrada.departamento", Value: 1}, {Key: "ts", Value: -1}}},
		},
		ColImpacto: {
			{Keys: bson.D{{Key: "modelo_version", Value: 1}, {Key: "ranking", Value: 1}}},
			{Keys: bson.D{{Key: "modelo_version", Value: 1}, {Key: "departamento", Value: 1}}},
		},
		ColMetricasCluster: {
			// Serie temporal: se borra sola a las 24 h (índice TTL).
			{Keys: bson.D{{Key: "ts", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(86400)},
			{Keys: bson.D{{Key: "nodo", Value: 1}, {Key: "ts", Value: -1}}},
		},
	}
	for col, modelos := range idx {
		if _, err := m.col(col).Indexes().CreateMany(ctx, modelos); err != nil {
			return fmt.Errorf("índices de %s: %w", col, err)
		}
	}
	return nil
}

// ---------- usuarios ----------

// CrearUsuarioSiNoExiste inserta el usuario solo si su username no existe.
func (m *Mongo) CrearUsuarioSiNoExiste(ctx context.Context, u Usuario) (bool, error) {
	res, err := m.col(ColUsuarios).UpdateOne(ctx, bson.M{"username": u.Username},
		bson.M{"$setOnInsert": u}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return false, err
	}
	return res.UpsertedCount == 1, nil
}

// BuscarUsuario devuelve el usuario por username.
func (m *Mongo) BuscarUsuario(ctx context.Context, username string) (*Usuario, error) {
	var u Usuario
	err := m.col(ColUsuarios).FindOne(ctx, bson.M{"username": username}).Decode(&u)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNoEncontrado
	}
	return &u, err
}

// ---------- modelos ----------

// SiguienteVersion devuelve max(_id)+1.
func (m *Mongo) SiguienteVersion(ctx context.Context) (int, error) {
	var ult Modelo
	err := m.col(ColModelos).FindOne(ctx, bson.M{}, options.FindOne().SetSort(bson.D{{Key: "_id", Value: -1}}).
		SetProjection(bson.M{"_id": 1})).Decode(&ult)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 1, nil
	}
	return ult.Version + 1, err
}

// GuardarModeloActivo inserta la versión y la marca como la única activa.
func (m *Mongo) GuardarModeloActivo(ctx context.Context, mod *Modelo) error {
	mod.Activo = true
	if _, err := m.col(ColModelos).UpdateMany(ctx, bson.M{"activo": true}, bson.M{"$set": bson.M{"activo": false}}); err != nil {
		return err
	}
	_, err := m.col(ColModelos).ReplaceOne(ctx, bson.M{"_id": mod.Version}, mod, options.Replace().SetUpsert(true))
	return err
}

// ModeloActivo devuelve la versión activa.
func (m *Mongo) ModeloActivo(ctx context.Context) (*Modelo, error) {
	var mod Modelo
	err := m.col(ColModelos).FindOne(ctx, bson.M{"activo": true}).Decode(&mod)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNoEncontrado
	}
	return &mod, err
}

// ListarModelos devuelve las últimas versiones sin el vector de pesos crudo.
func (m *Mongo) ListarModelos(ctx context.Context, limite int64) ([]Modelo, error) {
	cur, err := m.col(ColModelos).Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}).
		SetLimit(limite).SetProjection(bson.M{"w": 0, "pesos": 0}))
	if err != nil {
		return nil, err
	}
	out := []Modelo{}
	return out, cur.All(ctx, &out)
}

// ---------- entrenamientos ----------

// GuardarEntrenamiento crea o reemplaza el documento del trabajo.
func (m *Mongo) GuardarEntrenamiento(ctx context.Context, e *Entrenamiento) error {
	_, err := m.col(ColEntrenamientos).ReplaceOne(ctx, bson.M{"_id": e.ID}, e, options.Replace().SetUpsert(true))
	return err
}

// ObtenerEntrenamiento busca un trabajo por id.
func (m *Mongo) ObtenerEntrenamiento(ctx context.Context, id string) (*Entrenamiento, error) {
	var e Entrenamiento
	err := m.col(ColEntrenamientos).FindOne(ctx, bson.M{"_id": id}).Decode(&e)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNoEncontrado
	}
	return &e, err
}

// ListarEntrenamientos devuelve los últimos trabajos.
func (m *Mongo) ListarEntrenamientos(ctx context.Context, limite int64) ([]Entrenamiento, error) {
	cur, err := m.col(ColEntrenamientos).Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "creado", Value: -1}}).SetLimit(limite))
	if err != nil {
		return nil, err
	}
	out := []Entrenamiento{}
	return out, cur.All(ctx, &out)
}

// MarcarEntrenamientosHuerfanos pasa a error los trabajos que quedaron a
// medias si la API se reinició durante un entrenamiento.
func (m *Mongo) MarcarEntrenamientosHuerfanos(ctx context.Context) error {
	_, err := m.col(ColEntrenamientos).UpdateMany(ctx,
		bson.M{"estado": bson.M{"$nin": []string{EstadoCompletado, EstadoError}}},
		bson.M{"$set": bson.M{"estado": EstadoError, "error": "la API se reinició durante el entrenamiento"}})
	return err
}

// ---------- predicciones ----------

// InsertarPredicciones guarda un lote (escritura en bloque, sin orden).
func (m *Mongo) InsertarPredicciones(ctx context.Context, ps []Prediccion) error {
	if len(ps) == 0 {
		return nil
	}
	docs := make([]any, len(ps))
	for i := range ps {
		docs[i] = ps[i]
	}
	_, err := m.col(ColPredicciones).InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	return err
}

// ListarPredicciones devuelve las últimas predicciones (opcionalmente por departamento).
func (m *Mongo) ListarPredicciones(ctx context.Context, dept string, limite int64) ([]Prediccion, error) {
	filtro := bson.M{}
	if dept != "" {
		filtro["entrada.departamento"] = dept
	}
	cur, err := m.col(ColPredicciones).Find(ctx, filtro, options.Find().SetSort(bson.D{{Key: "ts", Value: -1}}).SetLimit(limite))
	if err != nil {
		return nil, err
	}
	out := []Prediccion{}
	return out, cur.All(ctx, &out)
}

// ContarPredicciones estima el total de la colección.
func (m *Mongo) ContarPredicciones(ctx context.Context) (int64, error) {
	return m.col(ColPredicciones).EstimatedDocumentCount(ctx)
}

// ---------- impacto ----------

// ReemplazarImpacto guarda el ranking completo de una versión del modelo.
func (m *Mongo) ReemplazarImpacto(ctx context.Context, version int, filas []Impacto) error {
	if _, err := m.col(ColImpacto).DeleteMany(ctx, bson.M{"modelo_version": version}); err != nil {
		return err
	}
	if len(filas) == 0 {
		return nil
	}
	docs := make([]any, len(filas))
	for i := range filas {
		docs[i] = filas[i]
	}
	_, err := m.col(ColImpacto).InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	return err
}

// ImpactoTop devuelve las n provincias con más casos graves esperados.
func (m *Mongo) ImpactoTop(ctx context.Context, version int, dept string, n int64) ([]Impacto, error) {
	filtro := bson.M{"modelo_version": version}
	if dept != "" {
		filtro["departamento"] = dept
	}
	cur, err := m.col(ColImpacto).Find(ctx, filtro, options.Find().SetSort(bson.D{{Key: "ranking", Value: 1}}).SetLimit(n))
	if err != nil {
		return nil, err
	}
	out := []Impacto{}
	return out, cur.All(ctx, &out)
}

// ImpactoPorDepartamento agrega el ranking por departamento (pipeline de agregación).
func (m *Mongo) ImpactoPorDepartamento(ctx context.Context, version int) ([]bson.M, error) {
	pipe := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"modelo_version": version}}},
		{{Key: "$group", Value: bson.M{
			"_id":              "$departamento",
			"positivos":        bson.M{"$sum": "$positivos"},
			"graves_esperados": bson.M{"$sum": "$graves_esperados"},
			"provincias":       bson.M{"$sum": 1},
		}}},
		{{Key: "$addFields", Value: bson.M{"tasa_por_1000": bson.M{"$multiply": bson.A{1000,
			bson.M{"$divide": bson.A{"$graves_esperados", "$positivos"}}}}}}},
		{{Key: "$sort", Value: bson.M{"graves_esperados": -1}}},
	}
	cur, err := m.col(ColImpacto).Aggregate(ctx, pipe)
	if err != nil {
		return nil, err
	}
	out := []bson.M{}
	return out, cur.All(ctx, &out)
}

// ---------- dataset y métricas ----------

// GuardarDataset reemplaza el resumen de la carga.
func (m *Mongo) GuardarDataset(ctx context.Context, d *Dataset) error {
	d.ID = "actual"
	_, err := m.col(ColDataset).ReplaceOne(ctx, bson.M{"_id": d.ID}, d, options.Replace().SetUpsert(true))
	return err
}

// ObtenerDataset devuelve el resumen de la carga.
func (m *Mongo) ObtenerDataset(ctx context.Context) (*Dataset, error) {
	var d Dataset
	err := m.col(ColDataset).FindOne(ctx, bson.M{"_id": "actual"}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNoEncontrado
	}
	return &d, err
}

// InsertarMetricasCluster guarda una muestra por nodo.
func (m *Mongo) InsertarMetricasCluster(ctx context.Context, ms []MetricaCluster) error {
	if len(ms) == 0 {
		return nil
	}
	docs := make([]any, len(ms))
	for i := range ms {
		docs[i] = ms[i]
	}
	_, err := m.col(ColMetricasCluster).InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	return err
}

// MetricasCluster devuelve las últimas muestras de un nodo (o de todos).
func (m *Mongo) MetricasCluster(ctx context.Context, nodo string, limite int64) ([]MetricaCluster, error) {
	filtro := bson.M{}
	if nodo != "" {
		filtro["nodo"] = nodo
	}
	cur, err := m.col(ColMetricasCluster).Find(ctx, filtro, options.Find().SetSort(bson.D{{Key: "ts", Value: -1}}).SetLimit(limite))
	if err != nil {
		return nil, err
	}
	out := []MetricaCluster{}
	return out, cur.All(ctx, &out)
}

// Conteos devuelve el número de documentos por colección (evidencia / panel).
func (m *Mongo) Conteos(ctx context.Context) map[string]int64 {
	out := map[string]int64{}
	for _, c := range []string{ColUsuarios, ColModelos, ColEntrenamientos, ColPredicciones, ColImpacto, ColDataset, ColMetricasCluster} {
		n, err := m.col(c).EstimatedDocumentCount(ctx)
		if err == nil {
			out[c] = n
		}
	}
	return out
}
