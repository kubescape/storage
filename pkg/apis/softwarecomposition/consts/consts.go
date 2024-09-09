package consts

type NetworkDirection string

const (
	Inbound  NetworkDirection = "inbound"
	Outbound NetworkDirection = "outbound"
)

type IsInternal string

const (
	True  IsInternal = "true"
	False IsInternal = "false"
)
