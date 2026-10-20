export namespace app {
	
	export class AttachmentView {
	    id: string;
	    label: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new AttachmentView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.url = source["url"];
	    }
	}
	export class ConfigInput {
	    chat_id: string;
	    title: string;
	    config: chat.Config;
	    expected_revision: number;
	
	    static createFrom(source: any = {}) {
	        return new ConfigInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.chat_id = source["chat_id"];
	        this.title = source["title"];
	        this.config = this.convertValues(source["config"], chat.Config);
	        this.expected_revision = source["expected_revision"];
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
	export class Controls {
	    can_submit: boolean;
	    can_capture: boolean;
	    can_edit_config: boolean;
	    can_cancel: boolean;
	    can_switch: boolean;
	    can_retry: boolean;
	    can_discard: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Controls(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.can_submit = source["can_submit"];
	        this.can_capture = source["can_capture"];
	        this.can_edit_config = source["can_edit_config"];
	        this.can_cancel = source["can_cancel"];
	        this.can_switch = source["can_switch"];
	        this.can_retry = source["can_retry"];
	        this.can_discard = source["can_discard"];
	    }
	}
	export class Draft {
	    text: string;
	    revision: number;
	
	    static createFrom(source: any = {}) {
	        return new Draft(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.revision = source["revision"];
	    }
	}
	export class DraftInput {
	    chat_id: string;
	    text: string;
	    revision: number;
	
	    static createFrom(source: any = {}) {
	        return new DraftInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.chat_id = source["chat_id"];
	        this.text = source["text"];
	        this.revision = source["revision"];
	    }
	}
	export class HistorySummary {
	    id: string;
	    title: string;
	    provider_id: string;
	    updated_at: string;
	    prepared: boolean;
	
	    static createFrom(source: any = {}) {
	        return new HistorySummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.provider_id = source["provider_id"];
	        this.updated_at = source["updated_at"];
	        this.prepared = source["prepared"];
	    }
	}
	export class Operation {
	    id: string;
	    kind: string;
	    phase: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Operation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.phase = source["phase"];
	        this.message = source["message"];
	    }
	}
	export class Snapshot {
	    revision: number;
	    active_chat_id: string;
	    title: string;
	    provider_id: string;
	    config: chat.Config;
	    config_revision: number;
	    messages: chat.Message[];
	    queue: AttachmentView[];
	    images: AttachmentView[];
	    draft: Draft;
	    operation: Operation;
	    controls: Controls;
	    settings: settings.Config;
	    provider: llm.Descriptor;
	    history: HistorySummary[];
	    error: string;
	    uncertain: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.revision = source["revision"];
	        this.active_chat_id = source["active_chat_id"];
	        this.title = source["title"];
	        this.provider_id = source["provider_id"];
	        this.config = this.convertValues(source["config"], chat.Config);
	        this.config_revision = source["config_revision"];
	        this.messages = this.convertValues(source["messages"], chat.Message);
	        this.queue = this.convertValues(source["queue"], AttachmentView);
	        this.images = this.convertValues(source["images"], AttachmentView);
	        this.draft = this.convertValues(source["draft"], Draft);
	        this.operation = this.convertValues(source["operation"], Operation);
	        this.controls = this.convertValues(source["controls"], Controls);
	        this.settings = this.convertValues(source["settings"], settings.Config);
	        this.provider = this.convertValues(source["provider"], llm.Descriptor);
	        this.history = this.convertValues(source["history"], HistorySummary);
	        this.error = source["error"];
	        this.uncertain = source["uncertain"];
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
	export class SubmitInput {
	    request_id: string;
	    intent_id: string;
	    chat_id: string;
	    text: string;
	    draft_revision: number;
	    attachment_ids: string[];
	    retry_message_id: string;
	
	    static createFrom(source: any = {}) {
	        return new SubmitInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.request_id = source["request_id"];
	        this.intent_id = source["intent_id"];
	        this.chat_id = source["chat_id"];
	        this.text = source["text"];
	        this.draft_revision = source["draft_revision"];
	        this.attachment_ids = source["attachment_ids"];
	        this.retry_message_id = source["retry_message_id"];
	    }
	}
	export class SubmitIntent {
	    id: string;
	    chat_id: string;
	    draft_revision: number;
	    expires_at: number;
	
	    static createFrom(source: any = {}) {
	        return new SubmitIntent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.chat_id = source["chat_id"];
	        this.draft_revision = source["draft_revision"];
	        this.expires_at = source["expires_at"];
	    }
	}

}

export namespace chat {
	
	export class Attempt {
	    id: string;
	    status: string;
	    config_revision: number;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new Attempt(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.status = source["status"];
	        this.config_revision = source["config_revision"];
	        this.error = source["error"];
	    }
	}
	export class Config {
	    context: string;
	    instructions: string;
	    response_language: string;
	    code_language: string;
	    detail: string;
	    model: string;
	    reasoning: string;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.context = source["context"];
	        this.instructions = source["instructions"];
	        this.response_language = source["response_language"];
	        this.code_language = source["code_language"];
	        this.detail = source["detail"];
	        this.model = source["model"];
	        this.reasoning = source["reasoning"];
	    }
	}
	export class Message {
	    id: string;
	    role: string;
	    text: string;
	    status: string;
	    attachment_ids: string[];
	    config_revision: number;
	    attempts: Attempt[];
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.role = source["role"];
	        this.text = source["text"];
	        this.status = source["status"];
	        this.attachment_ids = source["attachment_ids"];
	        this.config_revision = source["config_revision"];
	        this.attempts = this.convertValues(source["attempts"], Attempt);
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

export namespace llm {
	
	export class Capabilities {
	    images: boolean;
	    reasoning: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Capabilities(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.images = source["images"];
	        this.reasoning = source["reasoning"];
	    }
	}
	export class Descriptor {
	    id: string;
	    label: string;
	    capabilities: Capabilities;
	
	    static createFrom(source: any = {}) {
	        return new Descriptor(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.capabilities = this.convertValues(source["capabilities"], Capabilities);
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
	export class ReasoningEffort {
	    id: string;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new ReasoningEffort(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.description = source["description"];
	    }
	}
	export class Model {
	    id: string;
	    label: string;
	    description: string;
	    is_default: boolean;
	    default_reasoning: string;
	    reasoning_efforts: ReasoningEffort[];
	
	    static createFrom(source: any = {}) {
	        return new Model(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.description = source["description"];
	        this.is_default = source["is_default"];
	        this.default_reasoning = source["default_reasoning"];
	        this.reasoning_efforts = this.convertValues(source["reasoning_efforts"], ReasoningEffort);
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

export namespace settings {
	
	export class Config {
	    ui_scale: number;
	    theme: string;
	    font_size: number;
	    always_on_top: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ui_scale = source["ui_scale"];
	        this.theme = source["theme"];
	        this.font_size = source["font_size"];
	        this.always_on_top = source["always_on_top"];
	    }
	}

}

