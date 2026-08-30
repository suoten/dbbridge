export namespace main {
	
	export class GetTableSchemaResult {
	    success: boolean;
	    schema?: types.TableSchema;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new GetTableSchemaResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.schema = this.convertValues(source["schema"], types.TableSchema);
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class GetTablesResult {
	    success: boolean;
	    tables?: types.TableMeta[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new GetTablesResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.tables = this.convertValues(source["tables"], types.TableMeta);
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TestConnectionRequest {
	    type: string;
	    host: string;
	    port: number;
	    username: string;
	    password: string;
	    database: string;
	    sslMode: string;
	    charset: string;
	
	    static createFrom(source: any = {}) {
	        return new TestConnectionRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.database = source["database"];
	        this.sslMode = source["sslMode"];
	        this.charset = source["charset"];
	    }
	}
	export class StartMigrationRequest {
	    source: TestConnectionRequest;
	    target: TestConnectionRequest;
	    tables?: string[];
	    structureOnly: boolean;
	    dataOnly: boolean;
	    batchSize: number;
	    dropIfExists: boolean;
	    ignoreErrors: boolean;
	
	    static createFrom(source: any = {}) {
	        return new StartMigrationRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = this.convertValues(source["source"], TestConnectionRequest);
	        this.target = this.convertValues(source["target"], TestConnectionRequest);
	        this.tables = source["tables"];
	        this.structureOnly = source["structureOnly"];
	        this.dataOnly = source["dataOnly"];
	        this.batchSize = source["batchSize"];
	        this.dropIfExists = source["dropIfExists"];
	        this.ignoreErrors = source["ignoreErrors"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class TestConnectionResult {
	    success: boolean;
	    version: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new TestConnectionResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.version = source["version"];
	        this.error = source["error"];
	    }
	}

}

export namespace types {
	
	export class ColumnMeta {
	    name: string;
	    dataType: string;
	    baseType: string;
	    length?: number;
	    precision?: number;
	    scale?: number;
	    nullable: boolean;
	    defaultValue?: string;
	    autoIncrement: boolean;
	    comment?: string;
	    isPrimaryKey: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ColumnMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.dataType = source["dataType"];
	        this.baseType = source["baseType"];
	        this.length = source["length"];
	        this.precision = source["precision"];
	        this.scale = source["scale"];
	        this.nullable = source["nullable"];
	        this.defaultValue = source["defaultValue"];
	        this.autoIncrement = source["autoIncrement"];
	        this.comment = source["comment"];
	        this.isPrimaryKey = source["isPrimaryKey"];
	    }
	}
	export class ForeignKeyMeta {
	    name: string;
	    columns: string[];
	    refTable: string;
	    refColumns: string[];
	    onDelete?: string;
	    onUpdate?: string;
	
	    static createFrom(source: any = {}) {
	        return new ForeignKeyMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.columns = source["columns"];
	        this.refTable = source["refTable"];
	        this.refColumns = source["refColumns"];
	        this.onDelete = source["onDelete"];
	        this.onUpdate = source["onUpdate"];
	    }
	}
	export class IndexMeta {
	    name: string;
	    columns: string[];
	    isUnique: boolean;
	    isPrimary: boolean;
	    type?: string;
	
	    static createFrom(source: any = {}) {
	        return new IndexMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.columns = source["columns"];
	        this.isUnique = source["isUnique"];
	        this.isPrimary = source["isPrimary"];
	        this.type = source["type"];
	    }
	}
	export class TableReport {
	    tableName: string;
	    rows: number;
	    status: string;
	    error?: string;
	    duration: string;
	
	    static createFrom(source: any = {}) {
	        return new TableReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tableName = source["tableName"];
	        this.rows = source["rows"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.duration = source["duration"];
	    }
	}
	export class MigrationReport {
	    startTime: string;
	    endTime: string;
	    duration: string;
	    tablesTotal: number;
	    tablesSuccess: number;
	    tablesFailed: number;
	    totalRows: number;
	    failedTables?: string[];
	    tableDetails: TableReport[];
	
	    static createFrom(source: any = {}) {
	        return new MigrationReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.startTime = source["startTime"];
	        this.endTime = source["endTime"];
	        this.duration = source["duration"];
	        this.tablesTotal = source["tablesTotal"];
	        this.tablesSuccess = source["tablesSuccess"];
	        this.tablesFailed = source["tablesFailed"];
	        this.totalRows = source["totalRows"];
	        this.failedTables = source["failedTables"];
	        this.tableDetails = this.convertValues(source["tableDetails"], TableReport);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TableMeta {
	    name: string;
	    comment?: string;
	
	    static createFrom(source: any = {}) {
	        return new TableMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.comment = source["comment"];
	    }
	}
	
	export class TableSchema {
	    name: string;
	    comment?: string;
	    columns: ColumnMeta[];
	    indexes?: IndexMeta[];
	    foreignKeys?: ForeignKeyMeta[];
	    engine?: string;
	    charset?: string;
	    collation?: string;
	
	    static createFrom(source: any = {}) {
	        return new TableSchema(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.comment = source["comment"];
	        this.columns = this.convertValues(source["columns"], ColumnMeta);
	        this.indexes = this.convertValues(source["indexes"], IndexMeta);
	        this.foreignKeys = this.convertValues(source["foreignKeys"], ForeignKeyMeta);
	        this.engine = source["engine"];
	        this.charset = source["charset"];
	        this.collation = source["collation"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

