package chevalier

import (
	"encoding/json"
	"github.com/mattbaird/elastigo/api"
	es "github.com/mattbaird/elastigo/core"
	"github.com/mattbaird/elastigo/search"
	"log"
	"fmt"
	"strings"
	"time"
)

// QueryEngine presents an interface for running queries for sources
// against Elasticsearch.
type QueryEngine struct {
	indexName      string
	dataType       string
	nSources       int
	updateInterval time.Duration
}

// NewQueryEngine initializes a QueryEngine with the supplied
// Elasticsearch metadata. indexName and dataType can be anything as
// long as they're consistent.
func NewQueryEngine(host, indexName, dataType string) *QueryEngine {
	e := new(QueryEngine)
	e.indexName = indexName
	e.dataType = dataType
	api.Domain = host
	e.updateSourceCount()
	e.updateInterval = time.Second * 10
	go e.updateForever()
	return e
}

// sanitizeField takes an input field specifier (from a
// SourceRequest_Tag) and munges it so Elasticsearch likes it more. In
// particular, it makes single-wildcard queries work correctly.
//
// FIXME: mid-field wildcards still aren't working.
func (e *QueryEngine) sanitizeField(f string) string {
	f = strings.TrimSpace(f)
	if f == "*" {
		return fmt.Sprintf("%s._all", e.dataType)
	}
	f = fmt.Sprintf("%s.%s", e.dataType, f)
	// Need to escape wildcards here for some reason. 
	f = strings.Replace(f, "*", "\\*", -1)
	return f
}

// buildTagQuery constructs an elastigo query object (search.QueryDsl)
// from a SourceRequest_Tag, designed to be plugged into a
// query-string-type[0] query later on.
//
// [0]: http://www.elasticsearch.org/guide/en/elasticsearch/reference/current/query-dsl-query-string-query.html
func (e *QueryEngine) buildTagQuery(tag *SourceRequest_Tag) *search.QueryDsl {
	qs := new(search.QueryString)
	qs.Fields = make([]string, 0)
	qs.Fields = append(qs.Fields, e.sanitizeField(*tag.Field))
	qs.Query = *tag.Value
	q := search.Query().Qs(qs)
	return q
}

type SourceQuery map[string]interface{}

// BuildQuery takes a SourceRequest and turns it into a multi-level
// map suitable for marshalling to JSON and sending to Elasticsearch.
func (e *QueryEngine) BuildQuery(req *SourceRequest) SourceQuery {
	_ = search.Search(e.indexName).Type(e.dataType)
	tags := req.GetTags()
	tagQueries := make([]*search.QueryDsl, len(tags))
	for i, tag := range tags {
		tagQueries[i] = e.buildTagQuery(tag)
	}
	query := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": tagQueries,
			},
		},
	}
	return SourceQuery(query)
}

// runSourceRequest takes a request object and returns an elastigo-type
// (i.e., intermediate) result.
func (e *QueryEngine) runSourceRequest(req *SourceRequest) (*es.SearchResult, error) {
	q := e.BuildQuery(req)
	res, err := es.SearchRequest(false, e.indexName, e.dataType, q, "", 0)
	return &res, err
}

// GetSources takes a request object and returns the DataSourceBurst of
// the sources it gets back from Elasticsearch.
func (e *QueryEngine) GetSources(req *SourceRequest) (*DataSourceBurst, error) {
	res, err := e.runSourceRequest(req)
	if err != nil {
		return nil, err
	}
	sources := make([]*DataSource, len(res.Hits.Hits))
	for i, hit := range res.Hits.Hits {
		source := new(ElasticsearchSource)
		err = json.Unmarshal(hit.Source, source)
		if err != nil {
			return nil, err
		}
		sources[i] = source.Unmarshal()
	}
	burst := BuildSourceBurst(sources)
	return burst, nil
}

// FmtResult returns a string from a SearchResult by interpreting it in
// the most naive manner possible. For debugging.
func FmtResult(result *es.SearchResult) []string {
	results := make([]string, len(result.Hits.Hits))
	for i, hit := range result.Hits.Hits {
		results[i] = string(hit.Source[:])
	}
	return results
}

// updateSourceCount updates our running total of documents-in-index
// (by asking Elasticsearch).
func (e *QueryEngine) updateSourceCount() error {
	resp, err := es.Count(false, e.indexName, e.dataType)
	e.nSources = resp.Count
	return err
}

// updateForever updates the source counter on a regular basis.
func (e *QueryEngine) updateForever() {
	for true {
		time.Sleep(e.updateInterval)
		err := e.updateSourceCount()
		if err != nil {
			log.Printf("Error updating source count: %v", err)
		}
	}
}
