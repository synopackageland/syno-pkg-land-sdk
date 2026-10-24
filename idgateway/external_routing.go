package idgateway

func (f *Front) external() External {
	if f.cfg.External == nil {
		return External{}
	}
	return f.cfg.External()
}
