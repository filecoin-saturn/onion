package onion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	cid "github.com/ipfs/go-cid"
	"github.com/ipfs/go-unixfsnode/file"
	"github.com/ipld/go-car/v2/blockstore"
	dagpb "github.com/ipld/go-codec-dagpb"
	"github.com/ipld/go-ipld-prime"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipld/go-ipld-prime/storage/bsadapter"
	"io"
)

func ExtractRaw(carBytes []byte) (response []byte, err error) {
	cr := bytes.NewReader(carBytes)
	bs, err := blockstore.NewReadOnly(cr, nil)
	if err != nil {
		panic(err)
	}
	roots, err := bs.Roots()
	if err != nil {
		panic(err)
	}

	bsa := &bsadapter.Adapter{Wrapped: bs}
	ls := cidlink.DefaultLinkSystem()
	ls.TrustedStorage = true
	ls.SetReadStorage(bsa)

	if roots[0].Prefix().Codec == cid.Raw {
		blk, err := bs.Get(context.Background(), roots[0])
		if err != nil {
			panic(err)
		}
		return blk.RawData(), nil
	}

	return extractRoot(&ls, roots[0])
}

func extractRoot(ls *ipld.LinkSystem, root cid.Cid) (response []byte, err error) {
	if root.Prefix().Codec == cid.Raw {
		return nil, errors.New("raw cid not supported")
	}

	pbn, err := ls.Load(ipld.LinkContext{}, cidlink.Link{Cid: root}, dagpb.Type.PBNode)
	if err != nil {
		panic(err)
	}
	pbnode := pbn.(dagpb.PBNode)

	node, err := file.NewUnixFSFileWithPreload(context.Background(), pbnode, ls)
	if err != nil {
		fmt.Print("return 1 bye")
		return nil, err
	}
	nlr, err := node.AsLargeBytes()
	if err != nil {
		fmt.Print("return 2")
		return nil, err
	}

	resp := bytes.NewBuffer(nil)

	_, err = io.Copy(resp, nlr)
	if err != nil {
		fmt.Print("return 3")
		return nil, err
	}
	return resp.Bytes(), nil
}
