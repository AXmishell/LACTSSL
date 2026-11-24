//go:build !nobuiltin_dns && dns_providers_common && !dns_providers_minimal

package builtin

import _ "lactssl/providers/dns/lego5/groups/common"
