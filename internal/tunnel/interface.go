package tunnel

type TunnelOperation interface {
	CreateCmd() string
	DeleteCmd() string
}
